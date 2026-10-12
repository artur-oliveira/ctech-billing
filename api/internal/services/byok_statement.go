package services

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"gopkg.aoctech.app/api-commons/byok"
	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/lock"

	"gopkg.aoctech.app/billing/api/internal/byokrpc"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

var (
	ErrSyncTooSoon = errors.New("byok: synced less than 10 minutes ago")
	// errSpaceMismatch guards Sync against a space that is not the connection's.
	errSpaceMismatch = errors.New("byok: the space is not this connection's")
)

const (
	backfillWindowDays = 90
	incrementalDays    = 3
	manualSyncEvery    = 10 * time.Minute
)

type LineLinker interface {
	Link(ctx context.Context, sp space.ResolvedSpace, conn *byokconn.Connection, l repositories.BankLine) (bool, error)
}

type SyncResult struct {
	Posted, Skipped, Dropped int
	BackfillDone             bool
	Difference               billing.Cents
}

type ByokStatement struct {
	repo     *repositories.ByokRepository
	ledger   *repositories.LedgerRepository
	locks    *lock.Locker
	sessions func(*byokconn.Connection) byokrpc.Session
	record   func(context.Context, *byokconn.Connection, error, time.Time)
	links    LineLinker
}

func NewByokStatement(repo *repositories.ByokRepository, ledger *repositories.LedgerRepository, c cache.Backend,
	sessions func(*byokconn.Connection) byokrpc.Session, record func(context.Context, *byokconn.Connection, error, time.Time)) *ByokStatement {
	return &ByokStatement{repo: repo, ledger: ledger, locks: lock.New(c, 10*time.Minute), sessions: sessions, record: record}
}

func (s *ByokStatement) WithLinker(l LineLinker) *ByokStatement { s.links = l; return s }

func toByok(d brcal.Date) byok.Date   { return byok.Date{Year: d.Year, Month: d.Month, Day: d.Day} }
func fromByok(d byok.Date) brcal.Date { return brcal.Date{Year: d.Year, Month: d.Month, Day: d.Day} }

// StartBackfill runs the first window right after connect, detached from the
// request, bounded to 4 minutes; cmd/byok continues anything left. sp is the
// connecting request's space (already resolved: a value, safe to keep).
func (s *ByokStatement) StartBackfill(sp space.ResolvedSpace, conn *byokconn.Connection, now time.Time) {
	if s == nil {
		return
	}
	c := *conn
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		if _, err := s.Sync(ctx, sp, &c, now); err != nil {
			slog.WarnContext(ctx, "byok first backfill window", "connection_id", c.ID, "code", byok.ErrorCode(err))
		}
	}()
}

func (s *ByokStatement) SyncNow(ctx context.Context, sp space.ResolvedSpace, conn *byokconn.Connection, now time.Time) (SyncResult, error) {
	if now.Sub(conn.LastManualSyncAt) < manualSyncEvery {
		return SyncResult{}, ErrSyncTooSoon
	}
	conn.LastManualSyncAt = now.UTC()
	if err := s.repo.SaveConnection(ctx, conn, "", "", "", now); err != nil {
		return SyncResult{}, err
	}
	return s.Sync(ctx, sp, conn, now)
}

// Sync runs one step for one connection in sp, the connection's own space:
// the request's on "Sincronizar agora" and connect, space.ForJob in cmd/byok.
func (s *ByokStatement) Sync(ctx context.Context, sp space.ResolvedSpace, conn *byokconn.Connection, now time.Time) (SyncResult, error) {
	var res SyncResult
	if sp.OrganizationID() != conn.AccountOrganizationID || sp.Livemode() != conn.Livemode {
		return res, errSpaceMismatch
	}
	if conn.State != byokconn.StateActive || conn.SyncState == byokconn.SyncDisconnected || !conn.Has(byok.CapabilityStatement) {
		return res, nil // expired: sync paused (§ 3.5)
	}
	release, got, err := s.locks.Acquire(ctx, "byok:sync:"+conn.ID)
	if err != nil || !got {
		return res, err
	}
	defer release()
	sess := s.sessions(conn)
	today := brcal.FromTime(now)
	fail := func(err error) (SyncResult, error) { s.record(ctx, conn, err, now); return res, err }

	if !conn.BackfillDone {
		to := conn.BackfillFrom.AddDays(-1)
		from := to.AddDays(-(backfillWindowDays - 1))
		if from.Before(conn.FirstDay) {
			from = conn.FirstDay
		}
		refused := false
		if !to.Before(from) {
			if err := s.window(ctx, sp, sess, conn, from, to, &res); err != nil {
				if !errors.Is(err, byok.ErrInvalidRequest) && !errors.Is(err, byok.ErrNotFound) {
					return fail(err)
				}
				refused = true // spec § 8.5: a refused window ends the backfill
			} else {
				conn.BackfillFrom = from
			}
		}
		if refused || !conn.FirstDay.Before(conn.BackfillFrom) {
			conn.BackfillDone, conn.FirstDay = true, conn.BackfillFrom
			if conn.Livemode {
				bal, err := sess.Balance(ctx, toByok(conn.FirstDay.AddDays(-1)))
				if err != nil {
					return fail(err)
				}
				// A bank account that held nothing that day has no opening to post.
				if bal.Available != 0 {
					if _, err := s.ledger.PostProviderOpening(ctx, sp, conn.LedgerAccountID, billing.Cents(bal.Available), conn.FirstDay.AddDays(-1),
						repositories.PostMeta{Actor: "system:byok", Memo: "Saldo inicial (banco)"}, now); err != nil && !errors.Is(err, repositories.ErrOpeningExists) {
						return res, err
					}
				}
			}
		}
	}

	from := today.AddDays(-incrementalDays)
	if from.Before(conn.FirstDay) {
		from = conn.FirstDay
	}
	if err := s.window(ctx, sp, sess, conn, from, today, &res); err != nil {
		return fail(err)
	}
	acct, err := s.ledger.GetAccount(ctx, sp, conn.LedgerAccountID)
	if err != nil {
		return res, err
	}
	pb := repositories.ProviderBalance{Available: acct.Balance, At: today, SyncedAt: now.UTC()}
	if conn.Livemode {
		bal, err := sess.Balance(ctx, byok.Date{})
		if err != nil {
			return fail(err)
		}
		pb.Available, pb.Blocked = billing.Cents(bal.Available), billing.Cents(bal.Blocked)
		res.Difference = acct.Balance - pb.Available
		if res.Difference != 0 && conn.BackfillDone {
			slog.WarnContext(ctx, "byok ledger differs from the bank", "connection_id", conn.ID)
		}
	}
	if err := s.ledger.SetProviderBalance(ctx, sp, conn.LedgerAccountID, pb); err != nil {
		return res, err
	}
	s.record(ctx, conn, nil, now)
	conn.LastSyncOKAt, conn.SyncedThrough = now.UTC(), today
	res.BackfillDone = conn.BackfillDone
	return res, s.repo.SaveConnection(ctx, conn, "", "", "", now)
}

func (s *ByokStatement) window(ctx context.Context, sp space.ResolvedSpace, sess byokrpc.Session, conn *byokconn.Connection, from, to brcal.Date, res *SyncResult) error {
	for page := 0; ; page++ {
		p, err := sess.StatementPage(ctx, toByok(from), toByok(to), page)
		if err != nil {
			return err
		}
		for _, l := range p.Lines {
			if err := s.line(ctx, sp, conn, l, res); err != nil {
				return err
			}
		}
		if page+1 >= p.TotalPages {
			return nil
		}
	}
}

func (s *ByokStatement) line(ctx context.Context, sp space.ResolvedSpace, conn *byokconn.Connection, l byok.StatementLine, res *SyncResult) error {
	if !conn.Livemode {
		owner, found, err := s.repo.ChargeRefOwner(ctx, conn.Provider, false, l.ChargeRef)
		if err != nil {
			return err
		}
		if l.ChargeRef == "" || !found || owner != conn.AccountOrganizationID {
			res.Dropped++ // D22: the shared sandbox statement is filtered to the tenant's own charges
			return nil
		}
	}
	bl := repositories.BankLine{Provider: conn.Provider, LineID: l.ID, Date: fromByok(l.Date), Credit: l.Direction == byok.Credit,
		Amount: billing.Cents(l.Amount), Memo: memo(l), ChargeRef: l.ChargeRef, PaymentID: l.PaymentID}
	if s.links != nil && l.ChargeRef != "" {
		handled, err := s.links.Link(ctx, sp, conn, bl)
		if err != nil {
			return err
		}
		if handled {
			res.Skipped++
			return nil
		}
	}
	_, posted, err := s.ledger.PostBankLine(ctx, sp, conn.LedgerAccountID, bl, repositories.PostMeta{Actor: "system:byok"}, time.Now())
	if err != nil {
		return err
	}
	if posted {
		res.Posted++
	} else {
		res.Skipped++
	}
	return nil
}

func memo(l byok.StatementLine) string {
	if l.Description == "" {
		return l.Title
	}
	return l.Title + " · " + l.Description
}
