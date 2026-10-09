package repositories

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/space"
)

func TestLedgerKeys(t *testing.T) {
	sp, err := space.ForJob("USER#u1", true)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		LedgerSpaceSK():         "SPACE",
		LedgerAccountSK("bank"): "ACCOUNT#bank",
		LedgerSummarySK(finance.Month{Year: 2026, Month: time.March}, "bank"): "SUMMARY#2026-03#bank",
		LedgerTxSK("01J"):         "TX#01J",
		LedgerEntryPK(sp, "bank"): "USER#u1#live#ACCOUNT#bank",
		LedgerEntrySK(brcal.New(2026, time.March, 2), "01J", 1): "ENTRY#2026-03-02#01J#01",
		LedgerReversalSK("01J"):                                 "REVERSAL#01J",
		LedgerOpeningSK("bank"):                                 "OPENING#bank",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

// Every finance partition key begins with the space key (ADR 0003/0025).
func TestEveryLedgerPartitionBeginsWithTheSpaceKey(t *testing.T) {
	sp, _ := space.ForJob("0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", false)
	if !strings.HasPrefix(LedgerEntryPK(sp, "bank"), sp.PK()) {
		t.Fatal("entry partition does not begin with the space key")
	}
}

func TestCardKeys(t *testing.T) {
	sp, err := space.ForJob("USER#u1", true)
	if err != nil {
		t.Fatal(err)
	}
	mar := finance.Month{Year: 2026, Month: time.March}
	for got, want := range map[string]string{
		ItemSK(mar, "P1", "01"): "STATEMENT#2026-03#ITEM#P1#01",
		StatementSK(mar):        "STATEMENT#2026-03",
		CardSK("c"):             "CARD#c",
		PurchaseSK("p"):         "PURCHASE#p",
		ClosePK(true):           "live#finance-close",
	} {
		if got != want {
			t.Errorf("%q, want %q", got, want)
		}
	}
	if !strings.HasPrefix(CardPK(sp, "c"), sp.PK()) {
		t.Error("a card partition must begin with its space")
	}
}

func TestAStatementDescriptionFitsTheBillWhateverTheCardName(t *testing.T) {
	m := finance.Month{Year: 2026, Month: time.March}
	if got := statementDescription("Visa", m); got != "Fatura Visa 2026-03" {
		t.Fatalf("got %q", got)
	}
	long := statementDescription(strings.Repeat("💳", 80), m)
	if len(long) > 200 || !strings.HasSuffix(long, " 2026-03") || !utf8.ValidString(long) {
		t.Fatalf("%d bytes, valid=%v: %q", len(long), utf8.ValidString(long), long)
	}
}
