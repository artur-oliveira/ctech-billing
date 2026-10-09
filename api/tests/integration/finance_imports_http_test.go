//go:build integration

package integration

import (
	"encoding/base64"
	"testing"
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
