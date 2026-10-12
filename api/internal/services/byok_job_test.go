package services

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/api-commons/alerts"
	"gopkg.aoctech.app/api-commons/byok"
	"gopkg.aoctech.app/api-commons/cache"

	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/email"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// memJobRepo is the job's view of the byok table, in memory.
type memJobRepo struct {
	mu    sync.Mutex
	conns []byokconn.Connection
}

func (m *memJobRepo) ConnectionsForJob(_ context.Context, livemode bool, _ map[string]types.AttributeValue, _ int) (*repositories.Page[byokconn.Connection], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []byokconn.Connection
	for _, c := range m.conns {
		if c.Livemode == livemode {
			out = append(out, c)
		}
	}
	return &repositories.Page[byokconn.Connection]{Items: out}, nil
}

func (m *memJobRepo) SaveConnection(_ context.Context, c *byokconn.Connection, _, _, _ string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.conns {
		if m.conns[i].ID == c.ID {
			if m.conns[i].Revision != c.Revision {
				return repositories.ErrConnectionChanged
			}
			c.Revision++
			m.conns[i] = *c
			return nil
		}
	}
	return repositories.ErrNotFound
}

type recordingMailer struct {
	mu   sync.Mutex
	sent []email.ByokNotice
}

func (r *recordingMailer) SendByokNotice(_ context.Context, _ string, n email.ByokNotice) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, n)
	return nil
}

func (r *recordingMailer) kinds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, n := range r.sent {
		out = append(out, n.Kind)
	}
	return out
}

type recordingAlerts struct {
	mu  sync.Mutex
	got []alerts.Alert
}

func (r *recordingAlerts) Alert(_ context.Context, a alerts.Alert) {
	r.mu.Lock()
	r.got = append(r.got, a)
	r.mu.Unlock()
}

func jobSpaces(conn *byokconn.Connection) (space.ResolvedSpace, error) {
	return space.ForJob(conn.AccountOrganizationID, conn.Livemode)
}

func liveConn(id string) byokconn.Connection {
	return byokconn.Connection{AccountOrganizationID: "01a04ed6-1af9-745e-bcf7-d3b66fe52321", Livemode: true, Provider: "inter",
		ID: id, Kind: byokconn.KindMerchant, State: byokconn.StateActive, NotifyEmail: "financeiro@example.com",
		Capabilities: []byok.Capability{byok.CapabilityStatement}, Revision: 1}
}

func TestCertificateWarningsAreSentOnceAndResetByRenewal(t *testing.T) {
	ctx := context.Background()
	mail := &recordingMailer{}
	c := liveConn("c1")
	c.CertNotAfter = time.Now().AddDate(0, 0, 29)
	repo := &memJobRepo{conns: []byokconn.Connection{c}}
	j := NewByokJob(repo, nil, nil, mail, alerts.Nop{}, jobSpaces)
	for range 3 {
		if res := j.Run(ctx, true, time.Now()); len(res.Errors) != 0 {
			t.Fatal(res.Errors)
		}
	}
	if got := mail.kinds(); len(got) != 1 || got[0] != "cert_30" {
		t.Fatalf("%v", got)
	}
	renewed := repo.conns[0]
	renewed.ResetWarnings()
	renewed.CertNotAfter = time.Now().AddDate(1, 0, 0)
	repo.conns[0] = renewed
	j.Run(ctx, true, time.Now())
	if len(mail.kinds()) != 1 {
		t.Fatal("a renewed certificate is not warned")
	}
}

func TestAnExpiredCertificateExpiresTheConnectionAndWarnsOnce(t *testing.T) {
	mail := &recordingMailer{}
	c := liveConn("c1")
	c.CertNotAfter = time.Now().Add(-time.Hour)
	repo := &memJobRepo{conns: []byokconn.Connection{c}}
	j := NewByokJob(repo, nil, nil, mail, alerts.Nop{}, jobSpaces)
	j.Run(context.Background(), true, time.Now())
	j.Run(context.Background(), true, time.Now())
	if repo.conns[0].State != byokconn.StateExpired || len(mail.kinds()) != 1 || mail.kinds()[0] != "cert_expired" {
		t.Fatalf("%s %v", repo.conns[0].State, mail.kinds())
	}
}

func TestADegradedConnectionIsReportedOncePerDay(t *testing.T) {
	mail, al := &recordingMailer{}, &recordingAlerts{}
	c := liveConn("c1")
	c.ConsecutiveFailures = 3
	repo := &memJobRepo{conns: []byokconn.Connection{c}}
	j := NewByokJob(repo, nil, nil, mail, al, jobSpaces)
	day := time.Date(2026, 10, 11, 15, 0, 0, 0, time.UTC)
	j.Run(context.Background(), true, day)
	j.Run(context.Background(), true, day.Add(time.Hour))
	if got := mail.kinds(); len(got) != 1 || got[0] != "degraded" || len(al.got) != 1 {
		t.Fatalf("mail %v alerts %d", got, len(al.got))
	}
	j.Run(context.Background(), true, day.Add(24*time.Hour))
	if len(mail.kinds()) != 2 {
		t.Fatal("the next day reports again")
	}
}

func TestAFailingConnectionDoesNotStopTheOthers(t *testing.T) {
	repo := &memJobRepo{conns: []byokconn.Connection{liveConn("a"), liveConn("b"), liveConn("c")}}
	j := NewByokJob(repo, nil, nil, &recordingMailer{}, alerts.Nop{}, jobSpaces)
	var mu sync.Mutex
	seen := map[string]bool{}
	j.Register(StepSync, func(_ context.Context, _ space.ResolvedSpace, conn *byokconn.Connection, _ time.Time) error {
		mu.Lock()
		seen[conn.ID] = true
		mu.Unlock()
		if conn.ID == "b" {
			return byok.ErrUnavailable
		}
		return nil
	})
	res := j.Run(context.Background(), true, time.Now())
	if res.Connections != 3 || len(res.Errors) != 1 || !errors.Is(res.Errors[0], byok.ErrUnavailable) || len(seen) != 3 {
		t.Fatalf("%+v %v", res, seen)
	}
}

func TestTheSandboxCertificateWarnsTheOperatorOncePerThreshold(t *testing.T) {
	al := &recordingAlerts{}
	sb := liveConn("sb")
	sb.Livemode, sb.Kind = false, byokconn.KindSandboxShared
	repo := &memJobRepo{conns: []byokconn.Connection{sb, func() byokconn.Connection { c := sb; c.ID = "sb2"; return c }()}}
	notAfter := time.Now().Add(6 * 24 * time.Hour)
	j := NewByokJob(repo, nil, nil, &recordingMailer{}, al, jobSpaces).
		WithSandboxCertificate(func(context.Context, *byokconn.Connection) (time.Time, error) { return notAfter, nil }, cache.NewMemoryBackend(10))
	for range 3 {
		j.Run(context.Background(), false, time.Now())
	}
	if len(al.got) != 1 {
		t.Fatalf("%d alerts, want one (D-7) for one shared integration", len(al.got))
	}
}

// Final review I2: a certificate that expired makes the bank refuse the token,
// so sync fails; the expiry and its notice must not depend on sync succeeding.
func TestAnExpiredCertificateIsHandledEvenWhenTheSyncFails(t *testing.T) {
	mail := &recordingMailer{}
	c := liveConn("c1")
	c.CertNotAfter = time.Now().Add(-time.Hour)
	repo := &memJobRepo{conns: []byokconn.Connection{c}}
	j := NewByokJob(repo, nil, nil, mail, alerts.Nop{}, jobSpaces)
	j.Register(StepSync, func(context.Context, space.ResolvedSpace, *byokconn.Connection, time.Time) error {
		return byok.ErrCredentialsRefused
	})
	j.Run(context.Background(), true, time.Now())
	if repo.conns[0].State != byokconn.StateExpired || len(mail.kinds()) != 1 {
		t.Fatalf("state %s, mail %v", repo.conns[0].State, mail.kinds())
	}
}
