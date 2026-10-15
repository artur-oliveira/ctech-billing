//go:build integration

package integration

import (
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

var logT0 = time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)

func newScope(kind string) string { return kind + ":" + id.New() + ":live" }

func writeChange(t *testing.T, repo *repositories.RealtimeRepository, at time.Duration, entityID string, scopes ...string) string {
	t.Helper()
	c := &repositories.Change{Type: "invoice.paid", Entity: "invoice", EntityID: entityID, Status: "PAID", Scopes: scopes}
	if err := repo.Write(ctxT(t), c, logT0.Add(at)); err != nil {
		t.Fatal(err)
	}
	return c.ID
}

func idsOf(cs []repositories.Change) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.EntityID
	}
	return out
}

// RealtimeLog (a): the change is part of the mutation's transaction, so a
// mutation that fails leaves none, and one that commits leaves one per scope.
func TestRealtimeLogAFailedTransactionLeavesNoChange(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewRealtimeRepository(testDB, testCfg)
	rows := repositories.NewBase(testDB, testCfg, repositories.TableInvoices)
	tables := repositories.Tables{
		Rows:   rows,
		Audit:  repositories.NewBase(testDB, testCfg, repositories.TableAudit),
		Events: repositories.NewBase(testDB, testCfg, repositories.TableWebhooks),
	}
	scope := newScope("tenant")
	pk, sk := "TEST#"+id.New(), "ROW#1"
	if err := rows.PutItem(ctx, map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: pk}, "sk": &types.AttributeValueMemberS{Value: sk},
		"status": &types.AttributeValueMemberS{Value: "OPEN"},
	}); err != nil {
		t.Fatal(err)
	}
	change := func() *repositories.Change {
		return &repositories.Change{Type: "invoice.paid", Entity: "invoice", EntityID: "in_x", Status: "PAID", Scopes: []string{scope}}
	}
	commit := func(from string, c *repositories.Change) error {
		return repositories.CommitStatusChange(ctx, tables, repositories.StatusChange{
			OrganizationID: "o", Livemode: true, PK: pk, SK: sk, From: from, To: "PAID",
			Audit:    repositories.AuditEntry{Entity: repositories.EntityInvoice, EntityID: "in_x", Action: "x", Cause: "manual", Actor: "test"},
			Realtime: c,
		}, logT0)
	}

	if err := commit("PAID", change()); !errors.Is(err, repositories.ErrConcurrentModification) {
		t.Fatalf("a transition from the wrong state = %v, want ErrConcurrentModification", err)
	}
	head, err := repo.Head(ctx, []string{scope})
	if err != nil || head != "" {
		t.Fatalf("a failed transaction left a change: head=%q err=%v", head, err)
	}

	ok := change()
	if err := commit("OPEN", ok); err != nil {
		t.Fatal(err)
	}
	if head, _ := repo.Head(ctx, []string{scope}); head != ok.ID || ok.ID == "" {
		t.Fatalf("head = %q, want the committed change %q", head, ok.ID)
	}
}

// RealtimeLog (b)+(c): from the cursor, in order, with the 5 s overlap, deduplicated
// across scopes, limited with more=true.
func TestRealtimeLogSinceReplaysInOrderWithTheOverlapAndLimits(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewRealtimeRepository(testDB, testCfg)
	a, b := newScope("tenant"), newScope("user")

	writeChange(t, repo, 0, "c0", a)
	writeChange(t, repo, 1*time.Second, "c1", a)
	writeChange(t, repo, 8*time.Second, "c8", a, b) // in two scopes: still one change
	cursor := writeChange(t, repo, 10*time.Second, "c10", b)
	writeChange(t, repo, 20*time.Second, "c20", a)

	got, more, err := repo.Since(ctx, []string{a, b}, cursor, 10)
	if err != nil || more {
		t.Fatalf("since: more=%v err=%v", more, err)
	}
	// c8 is 2 s before the cursor, inside the 5 s overlap; c0 and c1 are not.
	if want := []string{"c8", "c10", "c20"}; !slices.Equal(idsOf(got), want) {
		t.Fatalf("changes = %v, want %v", idsOf(got), want)
	}
	if len(got[0].Scopes) != 2 {
		t.Fatalf("a change written to two scopes lost one: %+v", got[0].Scopes)
	}
	if got[1].Status != "PAID" || got[1].Entity != "invoice" || got[1].Revision == 0 {
		t.Fatalf("a change lost its state: %+v", got[1])
	}

	limited, more, err := repo.Since(ctx, []string{a, b}, cursor, 2)
	if err != nil || !more || !slices.Equal(idsOf(limited), []string{"c8", "c10"}) {
		t.Fatalf("limit 2: %v more=%v err=%v", idsOf(limited), more, err)
	}
}

// RealtimeLog (d): a scope hears only its own changes.
func TestRealtimeLogAScopeNeverSeesAnothers(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewRealtimeRepository(testDB, testCfg)
	mine, theirs := newScope("invoice"), newScope("invoice")
	cursor := writeChange(t, repo, 0, "mine", mine)
	writeChange(t, repo, 1*time.Second, "theirs", theirs)

	got, _, err := repo.Since(ctx, []string{mine}, cursor, 10)
	if err != nil || !slices.Equal(idsOf(got), []string{"mine"}) {
		t.Fatalf("changes = %v err=%v, want only mine", idsOf(got), err)
	}
	if head, _ := repo.Head(ctx, []string{mine}); head != cursor {
		t.Fatalf("head of my scope = %q, want %q", head, cursor)
	}
}

// RealtimeLog (e): the head is the newest id across the scopes, "" for none.
func TestRealtimeLogHeadIsTheNewestAcrossScopes(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewRealtimeRepository(testDB, testCfg)
	a, b, empty := newScope("tenant"), newScope("user"), newScope("space")
	writeChange(t, repo, 0, "old", a)
	newest := writeChange(t, repo, 30*time.Second, "new", b)

	if head, err := repo.Head(ctx, []string{a, b}); err != nil || head != newest {
		t.Fatalf("head = %q err=%v, want %q", head, err, newest)
	}
	if head, err := repo.Head(ctx, []string{empty}); err != nil || head != "" {
		t.Fatalf("an empty scope's head = %q err=%v", head, err)
	}
}

func TestRealtimeLogEmptyCursorIsAPositionAndAGarbageOneIsRefused(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewRealtimeRepository(testDB, testCfg)
	scope := newScope("tenant")
	writeChange(t, repo, 0, "c0", scope)

	if got, more, err := repo.Since(ctx, []string{scope}, "", 10); err != nil || more || len(got) != 0 {
		t.Fatalf("empty cursor: %v more=%v err=%v", got, more, err)
	}
	if _, _, err := repo.Since(ctx, []string{scope}, "garbage", 10); !errors.Is(err, repositories.ErrBadCursor) {
		t.Fatalf("garbage cursor: %v", err)
	}
}

// The items carry a TTL, so the log cannot grow without bound.
func TestRealtimeLogItemsExpireInFortyEightHours(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewRealtimeRepository(testDB, testCfg)
	scope := newScope("tenant")
	cid := writeChange(t, repo, 0, "c0", scope)

	base := repositories.NewBase(testDB, testCfg, repositories.TableWebhooks)
	out, err := base.QueryRaw(ctx, &dynamodb.QueryInput{
		TableName:                 aws.String(base.TableName),
		KeyConditionExpression:    aws.String("pk = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "RT#" + scope}},
	})
	if err != nil || len(out.Items) != 1 {
		t.Fatalf("items = %d err=%v", len(out.Items), err)
	}
	ttl, ok := out.Items[0]["ttl"].(*types.AttributeValueMemberN)
	if !ok || ttl.Value != strconv.FormatInt(logT0.Add(48*time.Hour).Unix(), 10) {
		t.Fatalf("ttl = %v, want 48 h after the write (%s)", out.Items[0]["ttl"], cid)
	}
}
