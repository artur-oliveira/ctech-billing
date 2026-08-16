package billing

import (
	"errors"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func fixedPrice(amount Cents) *Price {
	return &Price{
		ID: "price_fixed", ProductID: "prod_1", Type: PriceFixed, Currency: CurrencyBRL,
		UnitAmount: amount, Recurrence: monthly(), Timing: BillAdvance,
	}
}

func meteredPrice(unit Cents) *Price {
	return &Price{
		ID: "price_metered", ProductID: "prod_1", Type: PriceMetered, Currency: CurrencyBRL,
		UnitAmount: unit, Recurrence: monthly(), Timing: BillArrears,
	}
}

func TestPriceValidate(t *testing.T) {
	if err := fixedPrice(4990).Validate(); err != nil {
		t.Fatalf("a plain fixed price must be valid: %v", err)
	}
	if err := meteredPrice(15).Validate(); err != nil {
		t.Fatalf("a plain metered price must be valid: %v", err)
	}

	// A metered price billed in advance would have to guess the usage it charges
	// for — the invalid combination that produces a wrong invoice rather than an
	// error.
	bad := meteredPrice(15)
	bad.Timing = BillAdvance
	if err := bad.Validate(); !errors.Is(err, ErrInvalidPrice) {
		t.Fatalf("metered + advance must be rejected, got %v", err)
	}

	for _, mutate := range []func(*Price){
		func(p *Price) { p.Type = "tiered" },
		func(p *Price) { p.Currency = "USD" },
		func(p *Price) { p.UnitAmount = -1 },
		func(p *Price) { p.Recurrence = Recurrence{IntervalMonth, 0} },
		func(p *Price) { p.Timing = "whenever" },
		func(p *Price) { p.Metadata = Metadata{"": "x"} },
	} {
		p := fixedPrice(1000)
		mutate(p)
		if err := p.Validate(); !errors.Is(err, ErrInvalidPrice) {
			t.Errorf("%+v must be rejected, got %v", p, err)
		}
	}
}

func TestPriceChargeCeiling(t *testing.T) {
	if fixedPrice(MaxChargeCents).ExceedsChargeCeiling() {
		t.Error("a price exactly at the ceiling is allowed")
	}
	if !fixedPrice(MaxChargeCents + 1).ExceedsChargeCeiling() {
		t.Error("a price above the ceiling must be flagged")
	}
	// A metered unit price says nothing about the period total, so it cannot be
	// checked in advance — the wallet is the enforcement point.
	if meteredPrice(MaxChargeCents + 1).ExceedsChargeCeiling() {
		t.Error("a metered unit price must not be checked against the per-charge ceiling")
	}
}

func TestMaskedTaxID(t *testing.T) {
	cases := map[string]string{
		"12345678909":    "•••••••8909",
		"123.456.789-09": "•••••••8909", // formatting must not change the mask
		"12345678000199": "••••••••••0199",
		"123":            "•••",
		"":               "",
	}
	for in, want := range cases {
		if got := MaskedTaxID(in); got != want {
			t.Errorf("MaskedTaxID(%q) = %q, want %q", in, got, want)
		}
	}
	if strings.Contains(MaskedTaxID("12345678909"), "1234567") {
		t.Fatal("the mask must not leak the leading digits")
	}
}

func TestCustomerAnonymizeClearsMetadataToo(t *testing.T) {
	// Metadata is free-form and propagated in every webhook, so it is exactly
	// where undeclared PII ends up. Leaving it behind makes the erasure a
	// formality.
	c := &Customer{
		Name: "Fulana de Tal", Email: "f@example.com", TaxID: "12345678909",
		ExternalRef: "user_42", Metadata: Metadata{"cpf_titular": "12345678909"},
	}
	c.Anonymize()

	if c.Email != "" || c.TaxID != "" || c.ExternalRef != "" {
		t.Fatalf("identifying fields survived: %+v", c)
	}
	if c.Metadata != nil {
		t.Fatalf("metadata survived anonymization: %v", c.Metadata)
	}
	if !c.Anonymized {
		t.Fatal("the record must be marked anonymized")
	}
	if c.Name == "Fulana de Tal" {
		t.Fatal("the name survived")
	}
}

func TestUsageRecordValidate(t *testing.T) {
	ok := UsageRecord{SubscriptionItemID: "si_1", IdempotencyKey: "k1", Quantity: 10, OccurredAt: time.Now()}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a plain record must be valid: %v", err)
	}
	for _, mutate := range []func(*UsageRecord){
		func(u *UsageRecord) { u.SubscriptionItemID = "" },
		func(u *UsageRecord) { u.IdempotencyKey = "" },
		func(u *UsageRecord) { u.Quantity = -1 },
		func(u *UsageRecord) { u.OccurredAt = time.Time{} },
	} {
		u := ok
		mutate(&u)
		if err := u.Validate(); !errors.Is(err, ErrInvalidUsage) {
			t.Errorf("%+v must be rejected, got %v", u, err)
		}
	}
}

func TestSumUsageOnlyCountsThePeriod(t *testing.T) {
	p := march2026()
	at := func(d int, hour int) time.Time {
		return time.Date(2026, time.March, d, hour, 0, 0, 0, time.UTC)
	}
	records := []UsageRecord{
		{Quantity: 5, OccurredAt: at(1, 12)},
		{Quantity: 7, OccurredAt: at(31, 12)},
		{Quantity: 100, OccurredAt: time.Date(2026, time.April, 1, 12, 0, 0, 0, time.UTC)}, // next period
		{Quantity: 100, OccurredAt: time.Date(2026, time.February, 28, 12, 0, 0, 0, time.UTC)},
	}
	if got := SumUsage(records, p); got != 12 {
		t.Fatalf("SumUsage = %d, want 12", got)
	}
}

func TestSumUsageUsesSaoPauloDayForTheBoundary(t *testing.T) {
	// 1 March 02:30 UTC is still 28 February in São Paulo, so this consumption
	// belongs to February's invoice, not March's.
	p := march2026()
	r := UsageRecord{Quantity: 9, OccurredAt: time.Date(2026, time.March, 1, 2, 30, 0, 0, time.UTC)}
	if got := SumUsage([]UsageRecord{r}, p); got != 0 {
		t.Fatalf("a UTC-boundary record leaked into March: got %d", got)
	}
	if r.Date() != brcal.New(2026, time.February, 28) {
		t.Fatalf("Date() = %s, want 2026-02-28", r.Date())
	}
}

func TestDeduplicateUsage(t *testing.T) {
	p := march2026()
	on := func(day int) time.Time { return time.Date(2026, time.March, day, 12, 0, 0, 0, time.UTC) }
	records := []UsageRecord{
		{IdempotencyKey: "a", Quantity: 1, OccurredAt: on(2)},
		{IdempotencyKey: "b", Quantity: 2, OccurredAt: on(3)},
		{IdempotencyKey: "a", Quantity: 1, OccurredAt: on(2)}, // the retry
	}
	if got := SumUsage(records, p); got != 4 {
		t.Fatalf("without deduplication the retry is double-counted: got %d, want 4", got)
	}
	got := DeduplicateUsage(records)
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2", len(got))
	}
	if total := SumUsage(got, p); total != 3 {
		t.Fatalf("deduplicated total = %d, want 3", total)
	}
}

func TestSwapLinesAlwaysProducesTwoLines(t *testing.T) {
	p := march2026()
	old, upgraded := fixedPrice(31000), fixedPrice(62000)
	upgraded.ID = "price_pro"

	credit, charge := SwapLines(old, upgraded, "DF-e Basic", "DF-e Pro", p, brcal.New(2026, time.March, 16))

	if credit.Amount >= 0 {
		t.Fatalf("the credit line must be negative, got %s", credit.Amount)
	}
	if charge.Amount <= 0 {
		t.Fatalf("the charge line must be positive, got %s", charge.Amount)
	}
	if !credit.Proration || !charge.Proration {
		t.Fatal("both lines must be marked as proration")
	}
	if credit.Period.Start != brcal.New(2026, time.March, 16) || credit.Period.End != p.End {
		t.Fatalf("the credit line must carry the remainder period, got %+v", credit.Period)
	}
	if got := Subtotal([]InvoiceItem{credit, charge}); got != 16000 {
		t.Fatalf("subtotal = %s, want R$ 160,00", got)
	}
	// The descriptions must state the fraction, not just the result.
	if !strings.Contains(charge.Description, "16 de 31 dias") {
		t.Fatalf("the charge description must state the fraction: %q", charge.Description)
	}
}

func TestApplyToInvoice(t *testing.T) {
	p := march2026()
	line := FixedLine(fixedPrice(4990), "DF-e Basic", p, 1)

	inv := &Invoice{}
	if err := ApplyToInvoice(inv, []InvoiceItem{line}, 990); err != nil {
		t.Fatal(err)
	}
	if inv.Subtotal != 4990 || inv.Discount != 990 || inv.Total != 4000 {
		t.Fatalf("totals = %+v", inv)
	}

	if err := ApplyToInvoice(&Invoice{}, nil, 0); !errors.Is(err, ErrInvoiceItems) {
		t.Fatalf("an invoice with no lines must be rejected, got %v", err)
	}
	if err := ApplyToInvoice(&Invoice{}, []InvoiceItem{line}, -1); !errors.Is(err, ErrInvoiceItems) {
		t.Fatalf("a negative discount must be rejected, got %v", err)
	}
	// A negative total is a credit note, not an invoice.
	if err := ApplyToInvoice(&Invoice{}, []InvoiceItem{line}, 9999); !errors.Is(err, ErrInvoiceItems) {
		t.Fatalf("a negative total must be rejected, got %v", err)
	}
}

func TestGenerationKeyIsStableAndPerPeriod(t *testing.T) {
	// This is what makes the daily sweep safe to re-run: the same item and period
	// always produce the same key, so a second run writes nothing.
	first := GenerationKey("si_1", brcal.New(2026, time.March, 1))
	if first != GenerationKey("si_1", brcal.New(2026, time.March, 1)) {
		t.Fatal("the key must be deterministic")
	}
	if first == GenerationKey("si_1", brcal.New(2026, time.April, 1)) {
		t.Fatal("different periods must produce different keys")
	}
	if first == GenerationKey("si_2", brcal.New(2026, time.March, 1)) {
		t.Fatal("different items must produce different keys")
	}
	if first != "si_1:2026-03-01" {
		t.Fatalf("key = %q", first)
	}
}

func TestCreditNoteCannotExceedTheInvoiceTotal(t *testing.T) {
	inv := &Invoice{ID: "in_1", Status: InvoicePaid, Total: 10000}

	cn := &CreditNote{InvoiceID: "in_1", Amount: 4000}
	if err := cn.ValidateAgainst(inv, 0); err != nil {
		t.Fatalf("a partial credit must be allowed: %v", err)
	}
	if err := cn.ValidateAgainst(inv, 6000); err != nil {
		t.Fatalf("a credit that exactly fills the total must be allowed: %v", err)
	}
	if err := cn.ValidateAgainst(inv, 6001); !errors.Is(err, ErrInvalidCreditNote) {
		t.Fatalf("over-crediting must be rejected, got %v", err)
	}
}

func TestCreditNoteValidation(t *testing.T) {
	inv := &Invoice{ID: "in_1", Status: InvoiceOpen, Total: 10000}

	if err := (&CreditNote{InvoiceID: "in_1", Amount: 0}).ValidateAgainst(inv, 0); !errors.Is(err, ErrInvalidCreditNote) {
		t.Error("a zero credit must be rejected")
	}
	if err := (&CreditNote{InvoiceID: "in_1", Amount: -1}).ValidateAgainst(inv, 0); !errors.Is(err, ErrInvalidCreditNote) {
		t.Error("a negative credit must be rejected")
	}
	if err := (&CreditNote{InvoiceID: "in_other", Amount: 100}).ValidateAgainst(inv, 0); !errors.Is(err, ErrInvalidCreditNote) {
		t.Error("a credit for a different invoice must be rejected")
	}

	// A DRAFT invoice has not been issued — correct the lines instead. A VOID one
	// owes nothing.
	for _, status := range []InvoiceStatus{InvoiceDraft, InvoiceVoid} {
		draft := &Invoice{ID: "in_1", Status: status, Total: 10000}
		if err := (&CreditNote{InvoiceID: "in_1", Amount: 100}).ValidateAgainst(draft, 0); !errors.Is(err, ErrInvalidCreditNote) {
			t.Errorf("crediting a %s invoice must be rejected", status)
		}
	}
}

func TestFullyCreditedIsWhatRefundedMeans(t *testing.T) {
	inv := &Invoice{Total: 10000}
	if FullyCredited(inv, 9999) {
		t.Error("a partial credit is not a refund")
	}
	if !FullyCredited(inv, 10000) {
		t.Error("credits covering the total read as refunded")
	}
	if FullyCredited(&Invoice{Total: 0}, 0) {
		t.Error("a zero-total invoice is not refunded")
	}
}
