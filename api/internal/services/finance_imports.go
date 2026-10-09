package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/finance/statement"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// FinanceImports is F6: upload a statement, then reconcile its lines (spec
// § 3.7). The file is parsed here and dropped when the request ends; nothing
// settles until the person confirms a line.
type FinanceImports struct {
	repo  *repositories.ImportRepository
	bills *repositories.BillRepository
}

func NewFinanceImports(repo *repositories.ImportRepository, bills *repositories.BillRepository) *FinanceImports {
	return &FinanceImports{repo: repo, bills: bills}
}

// Upload parses the file and writes its lines. A CSV is read with the
// account's saved mapping, which must exist first.
func (s *FinanceImports) Upload(ctx context.Context, sp space.ResolvedSpace, accountID string, format statement.Format, file []byte, idempotencyKey string, now time.Time) (repositories.Import, error) {
	if err := sp.Require(space.Import); err != nil {
		return repositories.Import{}, err
	}
	var (
		parsed statement.Parsed
		err    error
	)
	switch format {
	case statement.FormatOFX:
		parsed, err = statement.ParseOFX(file)
	case statement.FormatCSV:
		m, merr := s.repo.GetMapping(ctx, sp, accountID)
		if errors.Is(merr, repositories.ErrNotFound) {
			return repositories.Import{}, statement.ErrNoMapping
		}
		if merr != nil {
			return repositories.Import{}, merr
		}
		parsed, err = statement.ParseCSV(file, m)
	default:
		return repositories.Import{}, fmt.Errorf("%w: unknown format %q", statement.ErrUnreadable, format)
	}
	if err != nil {
		return repositories.Import{}, err
	}
	return s.repo.Import(ctx, sp, accountID, format, parsed, idempotencyKey, now)
}

func (s *FinanceImports) List(ctx context.Context, sp space.ResolvedSpace, accountID string, now time.Time) ([]repositories.Import, error) {
	return s.repo.List(ctx, sp, accountID, now)
}

// LineView is a line with the open bills it may settle (pending lines only).
type LineView struct {
	repositories.ImportLine
	Candidates []finance.Bill
}

// maxOpenPages bounds the open-bill read behind the candidates: 10 pages of
// 100 per direction. A space with more open bills than that gets candidates
// from the earliest due thousand, which is where a statement's dates are.
const maxOpenPages = 10

// Get returns an import with every line and, for the pending ones, their
// candidates — computed on read from the open bills, never stored, so a bill
// paid elsewhere stops being offered at once.
func (s *FinanceImports) Get(ctx context.Context, sp space.ResolvedSpace, importID string, now time.Time) (repositories.Import, []LineView, error) {
	imp, lines, err := s.repo.Get(ctx, sp, importID, now)
	if err != nil {
		return repositories.Import{}, nil, err
	}
	var open []finance.Bill
	for _, l := range lines {
		if l.Status == repositories.LinePending {
			if open, err = s.openBills(ctx, sp); err != nil {
				return repositories.Import{}, nil, err
			}
			break
		}
	}
	return imp, Reconcile(imp.AccountID, lines, open, nil), nil
}

// Reconcile attaches candidates to the pending lines: the open bills it may
// settle first, then the bills auto-settle already paid that it may be linked
// to (exact amount only), at most statement.MaxCandidates in all.
func Reconcile(accountID string, lines []repositories.ImportLine, open, autoPaid []finance.Bill) []LineView {
	out := make([]LineView, len(lines))
	for i, l := range lines {
		out[i] = LineView{ImportLine: l}
		if l.Status == repositories.LinePending {
			sl := statement.Line{Date: l.Date, Amount: l.Amount}
			c := statement.Candidates(accountID, sl, open)
			c = append(c, statement.PaidCandidates(accountID, sl, autoPaid)...)
			out[i].Candidates = c[:min(len(c), statement.MaxCandidates)]
		}
	}
	return out
}

func (s *FinanceImports) openBills(ctx context.Context, sp space.ResolvedSpace) ([]finance.Bill, error) {
	var out []finance.Bill
	for _, dir := range []finance.Direction{finance.Payable, finance.Receivable} {
		page, err := s.bills.ListOpen(ctx, sp, dir, 100, nil)
		for n := 0; ; n++ {
			if err != nil {
				return nil, err
			}
			out = append(out, page.Items...)
			if len(page.LastEvaluatedKey) == 0 || n+1 >= maxOpenPages {
				break
			}
			page, err = s.bills.ListOpen(ctx, sp, dir, 100, page.LastEvaluatedKey)
		}
	}
	return out, nil
}

func (s *FinanceImports) Match(ctx context.Context, sp space.ResolvedSpace, importID string, n int, billID, differenceCategoryID, actor, requestID string, now time.Time) (repositories.ImportLine, finance.Bill, error) {
	return s.repo.Match(ctx, sp, importID, n, billID, differenceCategoryID, meta("import", actor, requestID), now)
}

func (s *FinanceImports) Create(ctx context.Context, sp space.ResolvedSpace, importID string, n int, categoryID, description, actor, requestID string, now time.Time) (repositories.ImportLine, finance.Bill, error) {
	return s.repo.Create(ctx, sp, importID, n, categoryID, description, meta("import", actor, requestID), now)
}

func (s *FinanceImports) Ignore(ctx context.Context, sp space.ResolvedSpace, importID string, n int, now time.Time) (repositories.ImportLine, error) {
	return s.repo.Ignore(ctx, sp, importID, n, now)
}

func (s *FinanceImports) Reopen(ctx context.Context, sp space.ResolvedSpace, importID string, n int, now time.Time) (repositories.ImportLine, error) {
	return s.repo.Reopen(ctx, sp, importID, n, now)
}

func (s *FinanceImports) GetMapping(ctx context.Context, sp space.ResolvedSpace, accountID string) (statement.Mapping, error) {
	return s.repo.GetMapping(ctx, sp, accountID)
}

func (s *FinanceImports) PutMapping(ctx context.Context, sp space.ResolvedSpace, accountID string, m statement.Mapping, now time.Time) error {
	return s.repo.PutMapping(ctx, sp, accountID, m, now)
}
