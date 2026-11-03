//go:build integration

package integration

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func interFile(rows ...string) string {
	csv := "\"Data\",\"Lançamento\",\"Categoria\",\"Tipo\",\"Valor\"\n" + strings.Join(rows, "\n") + "\n"
	return base64.StdEncoding.EncodeToString([]byte(csv))
}

func interRow(date, desc, cat, tipo, value string) string {
	return "\"" + strings.Join([]string{date, desc, cat, tipo, value}, "\",\"") + "\""
}

// The pinned clock is 2026-03-10: the card's open month is March 2026.
func TestCardImportOverHTTP(t *testing.T) {
	f := newFinanceEnv(t)
	var bank, food, card struct{ ID string }
	f.must(t, 201, "POST", "/accounts", `{"name":"Banco","class":"asset"}`, &bank)
	f.must(t, 201, "POST", "/accounts", `{"name":"Mercado","class":"expense","dre_group":"operating_expenses"}`, &food)
	f.must(t, 201, "POST", "/cards", fmt.Sprintf(`{"name":"Visa","closing_day":15,"due_day":25,"paying_account_id":%q}`, bank.ID), &card)
	f.must(t, 201, "POST", "/cards/"+card.ID+"/purchases",
		fmt.Sprintf(`{"date":"2026-03-09","description":"Mercado","category_id":%q,"total":1000,"installments":1}`, food.ID), nil)

	file := interFile(
		interRow("09/03/2026", "SUPERMERCADO X", "SUPERMERCADO", "Compra à vista", "R$ 10,00"),
		interRow("08/03/2026", "POSTO Y", "TRANSPORTE", "Compra à vista", "R$ 20,00"),
		interRow("06/03/2026", "PAGAMENTO ON LINE", "OUTROS", "Compra à vista", "-R$ 505,25"),
		interRow("31/02/2026", "BAD", "X", "Compra à vista", "R$ 1,00"),
	)
	up := func(content string) (int, string) {
		res := f.call(t, "POST", "/cards/"+card.ID+"/imports", fmt.Sprintf(`{"statement_month":"2026-03","bank":"inter","content":%q}`, content))
		return res.status, string(res.body)
	}
	var imp struct {
		ID            string
		Lines         int
		Duplicates    int
		RejectedCount int `json:"rejected_count"`
		Pending       int
		Kind          string
		Statement     string `json:"statement_month"`
	}
	f.must(t, 201, "POST", "/cards/"+card.ID+"/imports", fmt.Sprintf(`{"statement_month":"2026-03","bank":"inter","content":%q}`, file), &imp)
	if imp.ID == "" || imp.Lines != 3 || imp.RejectedCount != 1 || imp.Pending != 3 || imp.Statement != "2026-03" {
		t.Fatalf("import = %+v", imp)
	}
	if status, body := up(file); status != 200 {
		t.Fatalf("the same file again = %d %s, want 200 (nothing new)", status, body)
	}

	var detail struct {
		Import struct{ ID string }
		Lines  []struct {
			N      int
			Status string
		}
		Suggestions []struct {
			Kind  string
			Lines []int
			Item  *struct {
				PurchaseID string `json:"purchase_id"`
			}
		}
	}
	f.must(t, 200, "GET", "/card-imports/"+imp.ID, "", &detail)
	kinds := map[string]int{}
	for _, s := range detail.Suggestions {
		kinds[s.Kind]++
	}
	if len(detail.Lines) != 3 || kinds["pair"] != 1 || kinds["ignore"] != 1 {
		t.Fatalf("detail = %+v", detail)
	}

	var all struct{ Confirmed int }
	f.must(t, 200, "POST", "/card-imports/"+imp.ID+"/confirm-all", `{}`, &all)
	if all.Confirmed != 1 {
		t.Fatalf("confirm-all = %+v", all)
	}

	if res := f.call(t, "POST", "/card-imports/"+imp.ID+"/lines/2/create", `{}`); res.status != 422 || !strings.Contains(string(res.body), "category_unmapped") {
		t.Fatalf("create with no category = %d %s", res.status, res.body)
	}
	f.must(t, 200, "PUT", "/cards/"+card.ID+"/bank-categories", fmt.Sprintf(`{"categories":{"TRANSPORTE":%q}}`, food.ID), nil)
	var maps struct{ Categories map[string]string }
	f.must(t, 200, "GET", "/cards/"+card.ID+"/bank-categories", "", &maps)
	if maps.Categories["TRANSPORTE"] != food.ID {
		t.Fatalf("categories = %+v", maps)
	}
	var created struct {
		Line struct {
			Status     string
			PurchaseID string `json:"purchase_id"`
		}
	}
	f.must(t, 201, "POST", "/card-imports/"+imp.ID+"/lines/2/create", `{}`, &created)

	if created.Line.Status != "created" || created.Line.PurchaseID == "" {
		t.Fatalf("created = %+v", created)
	}
	if res := f.call(t, "POST", "/card-imports/"+imp.ID+"/lines/2/create", `{}`); res.status != 409 {
		t.Fatalf("a second create = %d %s", res.status, res.body)
	}
	f.must(t, 200, "POST", "/imports/"+imp.ID+"/lines/3/ignore", `{}`, nil)

	if res := f.call(t, "POST", "/cards/"+card.ID+"/imports", fmt.Sprintf(`{"statement_month":"2026-03","bank":"bb","content":%q}`, file)); res.status != 422 {
		t.Fatalf("another bank = %d %s", res.status, res.body)
	}
	f.must(t, 200, "POST", "/cards/"+card.ID+"/close", `{"month":"2026-03"}`, nil)
	other := interFile(interRow("07/03/2026", "OUTRO", "X", "Compra à vista", "R$ 5,00"))
	if status, body := up(other); status != 409 || !strings.Contains(body, "statement_not_open") {
		t.Fatalf("a closed statement = %d %s", status, body)
	}
}
