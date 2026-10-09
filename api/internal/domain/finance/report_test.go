package finance

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func day(m time.Month, dd int) brcal.Date { return brcal.New(2026, m, dd) }

func TestStatementOpeningAndClosing(t *testing.T) {
	// balance now 700: +1000 (Feb 20), -200 (Mar 5), -100 (Apr 2, future of the period)
	es := []Entry{
		{TxID: "a", Date: day(time.February, 20), Amount: 1000},
		{TxID: "b", Date: day(time.March, 5), Amount: -200},
		{TxID: "c", Date: day(time.April, 2), Amount: -100},
	}
	s := BuildStatement(700, es[1:], day(time.March, 1), day(time.April, 1))
	if s.Opening != 1000 || s.Closing != 800 || len(s.Lines) != 1 || s.Lines[0].Balance != 800 {
		t.Fatalf("statement = %+v", s)
	}
	// A period before every entry: opening 0, no lines.
	s = BuildStatement(700, es, day(time.January, 1), day(time.February, 1))
	if s.Opening != 0 || s.Closing != 0 || len(s.Lines) != 0 {
		t.Fatalf("early statement = %+v", s)
	}
}

func TestATransferIsNotCashFlow(t *testing.T) {
	es := []Entry{
		{AccountID: "bank", Date: day(time.March, 2), Amount: -500, Kind: KindTransfer, Flow: FlowNone},
		{AccountID: "cash", Date: day(time.March, 2), Amount: 500, Kind: KindTransfer, Flow: FlowNone},
	}
	r := CashFlow(map[string]billing.Cents{"bank": 0, "cash": 500}, es, MonthOf(day(time.March, 1)), MonthOf(day(time.March, 1)))
	if m := r.Months[0]; m.In != 0 || m.Out != 0 || len(m.ByFlow) != 0 {
		t.Fatalf("transfer showed up as flow: %+v", m)
	}
}

func TestCashFlowReconcilesWithTransfersAndOpenings(t *testing.T) {
	es := []Entry{
		{AccountID: "bank", Date: day(time.March, 1), Amount: 100000, Kind: KindOpeningBalance, Flow: FlowNone},
		{AccountID: "bank", Date: day(time.March, 5), Amount: 300000, Kind: KindSettlement, Flow: "salary"},
		{AccountID: "bank", Date: day(time.March, 10), Amount: -180000, Kind: KindSettlement, Flow: "rent"},
		{AccountID: "bank", Date: day(time.March, 12), Amount: -20000, Kind: KindTransfer, Flow: FlowNone},
		{AccountID: "cash", Date: day(time.March, 12), Amount: 20000, Kind: KindTransfer, Flow: FlowNone},
		{AccountID: "bank", Date: day(time.April, 3), Amount: -5000, Kind: KindSettlement, Flow: ""},
		{AccountID: "cash", Date: day(time.April, 5), Amount: -20000, Kind: KindOpeningBalance, Reversal: true, Flow: FlowNone},
	}
	bal := map[string]billing.Cents{"bank": 195000, "cash": 0}
	r := CashFlow(bal, es, MonthOf(day(time.March, 1)), MonthOf(day(time.April, 1)))
	var in, out, open billing.Cents
	for _, m := range r.Months {
		in, out, open = in+m.In, out+m.Out, open+m.Openings
	}
	if r.OpeningCash != 0 || r.ClosingCash != 195000 {
		t.Fatalf("opening/closing = %d/%d", r.OpeningCash, r.ClosingCash)
	}
	if r.OpeningCash+in-out+open != r.ClosingCash {
		t.Fatalf("does not reconcile: %d + %d - %d + %d != %d", r.OpeningCash, in, out, open, r.ClosingCash)
	}
	apr := r.Months[1]
	if len(apr.ByFlow) != 1 || apr.ByFlow[0].Flow != "" || apr.ByFlow[0].Amount != -5000 {
		t.Fatalf("legacy entry not under Sem categoria: %+v", apr.ByFlow)
	}
}

func TestDREFollowsTheGroupOrderAndSigns(t *testing.T) {
	mar := MonthOf(day(time.March, 1))
	sum := map[SummaryKey]Totals{
		{mar, "sales"}:    {Credits: 500000},
		{mar, "tax"}:      {Debits: 30000},
		{mar, "rent"}:     {Debits: 180000},
		{mar, "interest"}: {Debits: 1000},
		{mar, "yield"}:    {Credits: 4000},
		{mar, "bank"}:     {Debits: 999999}, // an asset: never in the DRE
	}
	acc := map[string]CategoryInfo{
		"sales": {ClassIncome, GroupGrossRevenue}, "tax": {ClassExpense, GroupDeductions},
		"rent": {ClassExpense, GroupOperatingExpenses}, "interest": {ClassExpense, GroupFinancialResult},
		"yield": {ClassIncome, GroupFinancialResult}, "bank": {ClassAsset, ""},
	}
	r := DRE(sum, acc, mar, mar)
	order := []DREGroup{GroupGrossRevenue, GroupDeductions, GroupOperatingExpenses, GroupFinancialResult}
	if len(r.Groups) != len(order) {
		t.Fatalf("groups = %+v", r.Groups)
	}
	for i, g := range r.Groups {
		if g.Group != order[i] {
			t.Fatalf("group %d = %s, want %s", i, g.Group, order[i])
		}
	}
	if r.Groups[3].Total != 3000 || r.Total != 500000-30000-180000+3000 {
		t.Fatalf("financial result %d, total %d", r.Groups[3].Total, r.Total)
	}
}

func TestAReversedPaymentLeavesNoTrace(t *testing.T) {
	mar := MonthOf(day(time.March, 1))
	es := []Entry{
		{AccountID: "bank", Date: day(time.March, 10), Amount: -180000, Kind: KindSettlement, Flow: "rent"},
		{AccountID: "bank", Date: day(time.March, 11), Amount: 180000, Kind: KindSettlement, Flow: "rent", Reversal: true},
	}
	m := CashFlow(map[string]billing.Cents{"bank": 0}, es, mar, mar).Months[0]
	if m.In != 0 || m.Out != 0 || len(m.ByFlow) != 0 {
		t.Fatalf("reversed payment left %+v", m)
	}
}

func TestDREPutsAnUngroupedCategoryUnderOther(t *testing.T) {
	mar := MonthOf(day(time.March, 1))
	r := DRE(map[SummaryKey]Totals{{mar, "misc"}: {Debits: 700}}, map[string]CategoryInfo{"misc": {ClassExpense, ""}}, mar, mar)
	if len(r.Groups) != 1 || r.Groups[0].Group != GroupOther || r.Total != -700 {
		t.Fatalf("ungrouped expense = %+v", r)
	}
}
