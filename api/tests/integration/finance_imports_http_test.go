//go:build integration

package integration

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// The whole F6 path through the real app: a base64 upload, the same file again,
// the import with its candidates, and a match.
func TestImportThroughTheAPI(t *testing.T) {
	f := newFinanceEnv(t)
	var bank, rent struct{ ID string }
	f.must(t, 201, "POST", "/accounts", `{"name":"Banco","class":"asset"}`, &bank)
	f.must(t, 201, "POST", "/accounts", `{"name":"Aluguel","class":"expense","dre_group":"operating_expenses"}`, &rent)
	var bill struct{ ID string }
	f.must(t, 201, "POST", "/bills", `{"direction":"payable","amount":100000,"account_id":"`+bank.ID+`","category_id":"`+rent.ID+`","due_date":"2026-03-10"}`, &bill)

	content := base64.StdEncoding.EncodeToString(threeLines)
	body := `{"account_id":"` + bank.ID + `","format":"ofx","content":"` + content + `"}`
	var first struct {
		ID         string
		Lines      int
		Duplicates int
		Pending    int
	}
	f.must(t, 201, "POST", "/imports", body, &first)
	if first.ID == "" || first.Lines != 3 || first.Pending != 3 {
		t.Fatalf("first upload = %+v", first)
	}
	var second struct {
		ID         string
		Lines      int
		Duplicates int
	}
	f.must(t, 200, "POST", "/imports", body, &second)
	if second.ID != "" || second.Lines != 0 || second.Duplicates != 3 {
		t.Fatalf("same file again = %+v", second)
	}

	var detail struct {
		Lines []struct {
			N          int
			Status     string
			Candidates []struct{ ID string }
		}
	}
	f.must(t, 200, "GET", "/imports/"+first.ID, "", &detail)
	if len(detail.Lines) != 3 || len(detail.Lines[0].Candidates) != 1 || detail.Lines[0].Candidates[0].ID != bill.ID {
		t.Fatalf("detail = %+v", detail)
	}
	f.must(t, 200, "POST", "/imports/"+first.ID+"/lines/1/match", `{"bill_id":"`+bill.ID+`"}`, nil)
	if res := f.call(t, "POST", "/imports/"+first.ID+"/lines/1/ignore", "{}"); res.status != 409 {
		t.Fatalf("ignoring a matched line = %d %s, want 409", res.status, res.body)
	}

	// A CSV upload before the account's columns are mapped is refused, by code.
	csv := base64.StdEncoding.EncodeToString([]byte("05/03/2026;Café;-5,00\n"))
	if res := f.call(t, "POST", "/imports", `{"account_id":"`+bank.ID+`","format":"csv","content":"`+csv+`"}`); res.status != 422 {
		t.Fatalf("csv with no mapping = %d %s", res.status, res.body)
	}
	f.must(t, 200, "PUT", "/accounts/"+bank.ID+"/csv-mapping",
		`{"delimiter":";","decimal":",","date_format":"dd/mm/yyyy","skip_rows":0,"date_column":1,"description_column":2,"amount_column":3,"debit_column":0}`, nil)
	f.must(t, 201, "POST", "/imports", `{"account_id":"`+bank.ID+`","format":"csv","content":"`+csv+`"}`, nil)
}

// 6.6 follow-up through the real app: a line's expiry is on the wire, a bill
// the job auto-settled is offered as already paid, linking it answers the
// linked line, and a second line asking for the same bill is a 409 by code.
func TestLinkThroughTheAPI(t *testing.T) {
	f := newFinanceEnv(t)
	var bank, rent struct{ ID string }
	f.must(t, 201, "POST", "/accounts", `{"name":"Banco","class":"asset"}`, &bank)
	f.must(t, 201, "POST", "/accounts", `{"name":"Aluguel","class":"expense","dre_group":"operating_expenses"}`, &rent)
	var bill struct{ ID string }
	f.must(t, 201, "POST", "/bills", `{"direction":"payable","amount":500,"account_id":"`+bank.ID+`","category_id":"`+rent.ID+`","due_date":"2026-03-10","auto_settle":true}`, &bill)
	recs := repositories.NewRecurrenceRepository(testDB, testCfg)
	_ = services.NewFinanceJobs(repositories.NewBillRepository(testDB, testCfg), recs).AutoSettle(context.Background(), true, brcal.New(2026, time.March, 20), time.Now())

	raw := syntheticOFX(ofxLine("A", "20260310", "-5.00", "Café"), ofxLine("B", "20260311", "-5.00", "Café"))
	var imp struct{ ID string }
	f.must(t, 201, "POST", "/imports", `{"account_id":"`+bank.ID+`","format":"ofx","content":"`+base64.StdEncoding.EncodeToString(raw)+`"}`, &imp)

	var detail struct {
		Lines []struct {
			N          int
			Status     string
			ExpiresAt  string `json:"expires_at"`
			Candidates []struct {
				ID       string
				Status   string
				PaidDate string `json:"paid_date"`
			}
		}
	}
	f.must(t, 200, "GET", "/imports/"+imp.ID, "", &detail)
	l1 := detail.Lines[0]
	// The app's test clock is fixed; the exact 90 days are pinned in TestALineSaysWhenItExpires.
	if exp, err := time.Parse(time.RFC3339, l1.ExpiresAt); err != nil || exp.Before(time.Date(2026, time.March, 10, 0, 0, 0, 0, time.UTC).Add(89*24*time.Hour)) {
		t.Fatalf("expires_at = %q, %v", l1.ExpiresAt, err)
	}
	if len(l1.Candidates) != 1 || l1.Candidates[0].ID != bill.ID || l1.Candidates[0].Status != "paid" || l1.Candidates[0].PaidDate != "2026-03-10" {
		t.Fatalf("line 1 = %+v", l1)
	}
	var linked struct {
		Line struct {
			Status string
			BillID string `json:"bill_id"`
		}
	}
	f.must(t, 200, "POST", "/imports/"+imp.ID+"/lines/1/link", `{"bill_id":"`+bill.ID+`"}`, &linked)
	if linked.Line.Status != "linked" || linked.Line.BillID != bill.ID {
		t.Fatalf("link = %+v", linked)
	}
	res := f.call(t, "POST", "/imports/"+imp.ID+"/lines/2/link", `{"bill_id":"`+bill.ID+`"}`)
	if res.status != 409 || !strings.Contains(string(res.body), `"bill_already_linked"`) {
		t.Fatalf("second link = %d %s, want 409 bill_already_linked", res.status, res.body)
	}
}
