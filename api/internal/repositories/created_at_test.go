package repositories

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
)

// A row embeds keys (created_at as a string) and a domain type that also tags
// created_at. Two fields with the same name at the same depth cancel out in
// the encoder, so the attribute was silently never written and every read came
// back as the zero time (01/01/1901 in the console).
func TestRowsKeepDomainCreatedAt(t *testing.T) {
	now := time.Date(2026, 10, 10, 15, 4, 5, 0, time.UTC)

	cn, err := Encode(creditNoteRow{
		keys:       newKeys("pk", "sk", RetentionAuditLog, now),
		CreditNote: billing.CreditNote{ID: "cn_1", CreatedAt: now},
		CreatedAt:  now,
	})
	if err != nil {
		t.Fatal(err)
	}
	gotCN, err := Decode[creditNoteRow](cn)
	if err != nil {
		t.Fatal(err)
	}
	if !gotCN.note().CreatedAt.Equal(now) {
		t.Fatalf("credit note created_at = %v, want %v", gotCN.note().CreatedAt, now)
	}

	au, err := buildAuditItem("org", true, AuditEntry{Entity: EntityAudit, EntityID: "x", Action: "a"}, "", "", now)
	if err != nil {
		t.Fatal(err)
	}
	gotAu, err := Decode[auditRow](au)
	if err != nil {
		t.Fatal(err)
	}
	if !gotAu.log().CreatedAt.Equal(now) {
		t.Fatalf("audit created_at = %v, want %v", gotAu.log().CreatedAt, now)
	}
}

// Rows written before the fix have no created_at at all; the id's ULID is the
// only record of when they were made.
func TestRowWithoutCreatedAtFallsBackToID(t *testing.T) {
	at := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	row := auditRow{AuditLog: billing.AuditLog{ID: id.PrefixAuditLog + id.NewAt(at)}}
	if got := row.log().CreatedAt; !got.Equal(at) {
		t.Fatalf("fallback = %v, want %v", got, at)
	}
	if got := (&creditNoteRow{}).note().CreatedAt; !got.IsZero() {
		t.Fatalf("no id, no time: got %v", got)
	}
}
