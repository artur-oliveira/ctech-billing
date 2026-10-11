//go:build integration

package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// cardRecFixture: a space with bank (asset), subs and food (expense) and a card
// "Visa" closing on the 25th, due on the 5th, added on 2026-03-01 (first and
// open month: March 2026).
type cardRecFixture struct {
	sp     space.ResolvedSpace
	ledger *repositories.LedgerRepository
	bills  *repositories.BillRepository
	cards  *repositories.CardRepository
	recs   *repositories.RecurrenceRepository
	card   repositories.CardRow
}

var cardRecNow = time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)

func newCardRecFixture(t *testing.T) cardRecFixture {
	t.Helper()
	f := cardRecFixture{sp: jobSpace(t, newSpaceOrgID(), true), ledger: repositories.NewLedgerRepository(testDB, testCfg),
		bills: repositories.NewBillRepository(testDB, testCfg), cards: repositories.NewCardRepository(testDB, testCfg),
		recs: repositories.NewRecurrenceRepository(testDB, testCfg)}
	ctx := context.Background()
	if err := f.ledger.EnsureSpace(ctx, f.sp, cardRecNow); err != nil {
		t.Fatal(err)
	}
	for _, a := range []finance.LedgerAccount{
		{ID: "bank", Name: "Banco", Class: finance.ClassAsset},
		{ID: "subs", Name: "Assinaturas", Class: finance.ClassExpense, Group: finance.GroupOperatingExpenses},
		{ID: "food", Name: "Mercado", Class: finance.ClassExpense, Group: finance.GroupOperatingExpenses},
	} {
		if err := f.ledger.CreateAccount(ctx, f.sp, a, cardRecNow); err != nil {
			t.Fatal(err)
		}
	}
	card, err := f.cards.CreateCard(ctx, f.sp, "Visa", finance.Card{ClosingDay: 25, DueDay: 5, PayingAccountID: "bank"}, cardRecNow)
	if err != nil {
		t.Fatal(err)
	}
	f.card = card
	return f
}

// cardRecurrence: monthly on `day`, since 2026-03-01, `n` installments.
func (f cardRecFixture) cardRecurrence(t *testing.T, day int, amount billing.Cents, n int) finance.Recurrence {
	t.Helper()
	rec, err := f.recs.Create(context.Background(), f.sp, finance.Recurrence{
		Direction: finance.Payable, Amount: amount, CategoryID: "subs", CardID: f.card.ID, Installments: n, Description: "Streaming",
		Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: day}, Start: brcal.New(2026, time.March, 1), Adjust: finance.AdjustNone},
	}, repositories.PostMeta{}, cardRecNow)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestACardRecurrenceIsStoredAndReadBack(t *testing.T) {
	f := newCardRecFixture(t)
	rec := f.cardRecurrence(t, 28, 3990, 2)
	got, cursor, err := f.recs.GetWithCursor(context.Background(), f.sp, rec.ID)
	if err != nil || got.CardID != f.card.ID || got.AccountID != "" || got.Installments != 2 || !cursor.IsZero() {
		t.Fatalf("read back %+v cursor %s, %v", got, cursor, err)
	}
}

func TestACardRecurrenceRefusesAnArchivedCardAndAnUnusableCategory(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	base := finance.Recurrence{Direction: finance.Payable, Amount: 3990, CategoryID: "subs", CardID: f.card.ID, Installments: 1,
		Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: 28}, Start: brcal.New(2026, time.March, 1), Adjust: finance.AdjustNone}}

	other := base
	other.CardID = "bank" // an asset account is not a card
	if _, err := f.recs.Create(ctx, f.sp, other, repositories.PostMeta{}, cardRecNow); !errors.Is(err, repositories.ErrUnknownAccount) {
		t.Fatalf("an account id as the card: %v", err)
	}
	wrongCat := base
	wrongCat.CategoryID = "bank"
	if _, err := f.recs.Create(ctx, f.sp, wrongCat, repositories.PostMeta{}, cardRecNow); !errors.Is(err, finance.ErrInvalidRecurrence) {
		t.Fatalf("an asset as the category: %v", err)
	}
	foreign := jobSpace(t, newSpaceOrgID(), true)
	if err := f.ledger.EnsureSpace(ctx, foreign, cardRecNow); err != nil {
		t.Fatal(err)
	}
	if _, err := f.recs.Create(ctx, foreign, base, repositories.PostMeta{}, cardRecNow); !errors.Is(err, repositories.ErrUnknownAccount) {
		t.Fatalf("another space's card: %v", err)
	}
	if err := f.ledger.ArchiveAccount(ctx, f.sp, f.card.ID, cardRecNow); err != nil {
		t.Fatal(err)
	}
	if _, err := f.recs.Create(ctx, f.sp, base, repositories.PostMeta{}, cardRecNow); !errors.Is(err, finance.ErrInvalidRecurrence) {
		t.Fatalf("an archived card: %v", err)
	}
}

func TestSwitchingARecurrenceBetweenAccountAndCardIsAPatch(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	rec, err := f.recs.Create(ctx, f.sp, finance.Recurrence{
		Direction: finance.Payable, Amount: 3990, CategoryID: "subs", AccountID: "bank", AutoSettle: true,
		Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: 28}, Start: brcal.New(2026, time.March, 1), Adjust: finance.AdjustNone},
	}, repositories.PostMeta{}, cardRecNow)
	if err != nil {
		t.Fatal(err)
	}
	card := f.card.ID
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{CardID: &card}, cardRecNow); err != nil {
		t.Fatal(err)
	}
	got, _ := f.recs.Get(ctx, f.sp, rec.ID)
	if got.CardID != card || got.AccountID != "" || got.AutoSettle || got.Installments != 1 {
		t.Fatalf("after switching to the card: %+v (auto_settle cleared, installments 1)", got)
	}
	bank, three := "bank", 3
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{CardID: &card, AccountID: &bank}, cardRecNow); !errors.Is(err, finance.ErrInvalidRecurrence) {
		t.Fatalf("both targets in one patch: %v", err)
	}
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{Installments: &three}, cardRecNow); err != nil {
		t.Fatal(err)
	}
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{AccountID: &bank}, cardRecNow); err != nil {
		t.Fatal(err)
	}
	got, _ = f.recs.Get(ctx, f.sp, rec.ID)
	if got.AccountID != "bank" || got.CardID != "" || got.Installments != 0 {
		t.Fatalf("after switching back: %+v", got)
	}
	yes := true
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{CardID: &card, AutoSettle: &yes}, cardRecNow); !errors.Is(err, finance.ErrInvalidRecurrence) {
		t.Fatalf("auto_settle sent with a card: %v", err)
	}
}

func TestNeedsAttentionIsSetByTheJobAndClearedByAnEditOrARun(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	rec := f.cardRecurrence(t, 28, 3990, 1)
	if err := f.recs.FlagAttention(ctx, f.sp, rec.ID, finance.AttentionCardArchived, cardRecNow); err != nil {
		t.Fatal(err)
	}
	got, _ := f.recs.Get(ctx, f.sp, rec.ID)
	if got.NeedsAttention != finance.AttentionCardArchived {
		t.Fatalf("needs_attention = %q", got.NeedsAttention)
	}
	desc := "Streaming HD"
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{Description: &desc}, cardRecNow); err != nil {
		t.Fatal(err)
	}
	if got, _ = f.recs.Get(ctx, f.sp, rec.ID); got.NeedsAttention != "" {
		t.Fatalf("an edit must clear it, got %q", got.NeedsAttention)
	}
	_ = f.recs.FlagAttention(ctx, f.sp, rec.ID, finance.AttentionCategoryArchived, cardRecNow)
	if err := f.recs.MarkMaterialised(ctx, f.sp, rec.ID, brcal.New(2026, time.March, 28), cardRecNow); err != nil {
		t.Fatal(err)
	}
	if got, _ = f.recs.Get(ctx, f.sp, rec.ID); got.NeedsAttention != "" {
		t.Fatalf("a run that moved the cursor must clear it, got %q", got.NeedsAttention)
	}
}
func (f cardRecFixture) charge(rec finance.Recurrence, nominal brcal.Date) finance.CardCharge {
	return finance.CardCharge{RecurrenceID: rec.ID, Nominal: nominal, CardID: rec.CardID, CategoryID: rec.CategoryID,
		Description: rec.Description, Amount: rec.Amount, Installments: rec.Installments}
}

func TestAnOccurrenceIsPostedOnceHoweverOftenAndConcurrentlyItIsAsked(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	rec := f.cardRecurrence(t, 28, 3990, 1)
	ch := f.charge(rec, brcal.New(2026, time.March, 28))
	meta := repositories.PostMeta{Origin: "recurrence", Actor: "scheduler"}

	var wg sync.WaitGroup
	created := make([]bool, 4)
	for i := range created {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			created[i], _, _ = f.cards.PostOccurrence(ctx, f.sp, ch, meta, time.Now())
		}(i)
	}
	wg.Wait()
	// A concurrent loser may answer a transaction conflict instead of a skip; the
	// repeat after them must then post it. Across all calls: exactly one created.
	again, _, err := f.cards.PostOccurrence(ctx, f.sp, ch, meta, time.Now())
	if err != nil {
		t.Fatalf("a later repeat: %v", err)
	}
	n := 0
	for _, c := range append(created, again) {
		if c {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d calls reported created, want exactly 1", n)
	}
	if third, _, err := f.cards.PostOccurrence(ctx, f.sp, ch, meta, time.Now()); err != nil || third {
		t.Fatalf("one more repeat = %v, %v; want (false, nil)", third, err)
	}
	if got := balance(t, f.ledger, f.sp, "subs"); got != 3990 {
		t.Fatalf("subs = %d, want the expense once", got)
	}
	apr, _ := f.cards.GetStatement(ctx, f.sp, f.card.ID, finance.Month{Year: 2026, Month: time.April})
	if len(apr.Items) != 1 || apr.Items[0].RecurrenceID != rec.ID || apr.Total != 3990 {
		t.Fatalf("April = %+v: the 28th is after the closing day (25), one item carrying its recurrence", apr)
	}
}

func TestAnOccurrenceIsNeverPostedOnTwoCardsOrAsBothABillAndAPurchase(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	rec := f.cardRecurrence(t, 28, 3990, 1)
	ch := f.charge(rec, brcal.New(2026, time.March, 28))
	if ok, _, err := f.cards.PostOccurrence(ctx, f.sp, ch, repositories.PostMeta{}, time.Now()); err != nil || !ok {
		t.Fatalf("first post = %v, %v", ok, err)
	}
	second, err := f.cards.CreateCard(ctx, f.sp, "Master", finance.Card{ClosingDay: 10, DueDay: 20, PayingAccountID: "bank"}, cardRecNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{CardID: &second.ID}, cardRecNow); err != nil {
		t.Fatal(err)
	}
	moved := ch
	moved.CardID = second.ID
	if ok, _, err := f.cards.PostOccurrence(ctx, f.sp, moved, repositories.PostMeta{}, time.Now()); err != nil || ok {
		t.Fatalf("the same occurrence on the new card = %v, %v; want (false, nil): the lock is per occurrence, not per card", ok, err)
	}
}

func TestAStaleChargeIsRefusedAsARecurrenceChange(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	rec := f.cardRecurrence(t, 28, 3990, 1)
	amount := billing.Cents(4990)
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{Amount: &amount}, cardRecNow); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.cards.PostOccurrence(ctx, f.sp, f.charge(rec, brcal.New(2026, time.March, 28)), repositories.PostMeta{}, time.Now()); !errors.Is(err, repositories.ErrRecurrenceChanged) {
		t.Fatalf("a charge read before the amount changed: %v, want ErrRecurrenceChanged", err)
	}
	if got := balance(t, f.ledger, f.sp, "subs"); got != 0 {
		t.Fatalf("subs = %d, nothing may post", got)
	}
}

func TestAnArchivedCardOrCategoryIsToldApart(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	rec := f.cardRecurrence(t, 28, 3990, 1)
	ch := f.charge(rec, brcal.New(2026, time.March, 28))
	if err := f.ledger.ArchiveAccount(ctx, f.sp, "subs", cardRecNow); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.cards.PostOccurrence(ctx, f.sp, ch, repositories.PostMeta{}, time.Now()); !errors.Is(err, repositories.ErrCategoryUnusable) {
		t.Fatalf("archived category: %v", err)
	}
	if err := f.ledger.ArchiveAccount(ctx, f.sp, f.card.ID, cardRecNow); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.cards.PostOccurrence(ctx, f.sp, ch, repositories.PostMeta{}, time.Now()); !errors.Is(err, repositories.ErrCardArchived) {
		t.Fatalf("archived card: %v", err)
	}
	// The HTTP purchase route keeps its codes: still ErrUnknownAccount underneath.
	if _, err := f.cards.AddPurchase(ctx, f.sp, f.card.ID, repositories.Purchase{Description: "x", CategoryID: "food", Date: brcal.New(2026, time.March, 2),
		Total: 100, Installments: make([]finance.Installment, 1)}, repositories.PostMeta{}, time.Now()); !errors.Is(err, repositories.ErrUnknownAccount) {
		t.Fatalf("AddPurchase on an archived card: %v", err)
	}
}

// Review Focus 4.
func TestAnOccurrenceWithoutDescriptionIsTitledByItsCategory(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	rec, err := f.recs.Create(ctx, f.sp, finance.Recurrence{Direction: finance.Payable, Amount: 1000, CategoryID: "subs", CardID: f.card.ID, Installments: 1,
		Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: 5}, Start: brcal.New(2026, time.March, 1), Adjust: finance.AdjustNone}},
		repositories.PostMeta{}, cardRecNow)
	if err != nil {
		t.Fatal(err)
	}
	_, p, err := f.cards.PostOccurrence(ctx, f.sp, f.charge(rec, brcal.New(2026, time.March, 5)), repositories.PostMeta{}, time.Now())
	if err != nil || p.Description != "Assinaturas" {
		t.Fatalf("purchase = %+v, %v; want it titled by its category", p, err)
	}
}

func TestTheHistoryListsPostedOccurrencesAndEndingSkipsThem(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	rec := f.cardRecurrence(t, 28, 30000, 3)
	if _, _, err := f.cards.PostOccurrence(ctx, f.sp, f.charge(rec, brcal.New(2026, time.March, 28)), repositories.PostMeta{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	hist, err := f.bills.ForRecurrence(ctx, f.sp, rec.ID, 12)
	if err != nil || len(hist) != 1 || hist[0].Purchase == nil {
		t.Fatalf("history = %+v, %v", hist, err)
	}
	if p := hist[0].Purchase; p.CardID != f.card.ID || p.Statement != (finance.Month{Year: 2026, Month: time.April}) || p.Amount != 30000 || p.ID == "" {
		t.Fatalf("posted occurrence = %+v", p)
	}
	made, err := f.bills.MadeAfter(ctx, f.sp, rec.ID, brcal.New(2026, time.March, 1))
	if err != nil || len(made) != 0 {
		t.Fatalf("MadeAfter = %+v, %v: a purchase is not a bill an end could cancel", made, err)
	}
}

func TestACardKnowsItsFirstMonth(t *testing.T) {
	f := newCardRecFixture(t)
	got, err := f.cards.GetCard(context.Background(), f.sp, f.card.ID)
	if err != nil || got.FirstMonth != (finance.Month{Year: 2026, Month: time.March}) {
		t.Fatalf("first month = %v, %v", got.FirstMonth, err)
	}
}
func (f cardRecFixture) jobs() *services.FinanceJobs {
	return services.NewFinanceJobs(f.bills, f.recs).WithCards(f.cards)
}

func at(y int, m time.Month, d int) (brcal.Date, time.Time) {
	return brcal.New(y, m, d), time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
}

func (f cardRecFixture) items(t *testing.T, m time.Month) []repositories.StatementItem {
	t.Helper()
	s, err := f.cards.GetStatement(context.Background(), f.sp, f.card.ID, finance.Month{Year: 2026, Month: m})
	if err != nil {
		t.Fatal(err)
	}
	return s.Items
}

// Spec § 6.1. The work lists are shared with every test in the run: assert on
// this space's rows only, never on the job's counters.
func TestACardRecurrencePostsOnePurchasePerOccurrenceAcrossRunsAndARace(t *testing.T) {
	f := newCardRecFixture(t)
	f.cardRecurrence(t, 28, 3990, 1)
	job := f.jobs()
	today, now := at(2026, time.March, 28)
	job.Materialise(context.Background(), true, today, now)
	job.Materialise(context.Background(), true, today, now)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); job.Materialise(context.Background(), true, today, now) }()
	}
	wg.Wait()
	job.Materialise(context.Background(), true, today, now) // heals a run that lost a transaction conflict
	if got := len(f.items(t, time.April)); got != 1 {
		t.Fatalf("April holds %d items, want exactly one purchase", got)
	}
	if got := balance(t, f.ledger, f.sp, "subs"); got != 3990 {
		t.Fatalf("subs = %d, want one expense", got)
	}
}

// Spec § 6.2.
func TestAnOccurrenceAfterTheClosingDayLandsOnNextMonthAndTheProjectionFollows(t *testing.T) {
	f := newCardRecFixture(t)
	f.cardRecurrence(t, 28, 3990, 1)
	job := f.jobs()
	before, _ := at(2026, time.March, 20)
	p, err := job.Project(context.Background(), f.sp, before, 4)
	if err != nil {
		t.Fatal(err)
	}
	if p.Months[2].Month.Month != time.May || p.Months[2].VirtualPayable != 3990 || p.Months[2].Payable != 0 {
		t.Fatalf("May while virtual = %+v", p.Months[2])
	}
	today, now := at(2026, time.March, 28)
	job.Materialise(context.Background(), true, today, now)
	if got := f.items(t, time.April); len(got) != 1 || got[0].Date != today {
		t.Fatalf("April = %+v: the 28th is after the 25th", got)
	}
	p, _ = job.Project(context.Background(), f.sp, today, 4)
	if p.Months[2].Payable != 3990 || p.Months[2].VirtualPayable != 0 {
		t.Fatalf("May once posted = %+v: Payable only", p.Months[2])
	}
}

// Spec § 6.3.
func TestAThreeInstallmentRecurrenceSpreadsOverThreeStatements(t *testing.T) {
	f := newCardRecFixture(t)
	f.cardRecurrence(t, 20, 30000, 3)
	today, now := at(2026, time.March, 20)
	f.jobs().Materialise(context.Background(), true, today, now)
	for _, m := range []time.Month{time.March, time.April, time.May} {
		if got := f.items(t, m); len(got) != 1 || got[0].Amount != 10000 || got[0].Of != 3 {
			t.Fatalf("%s = %+v, want one installment of 100,00", m, got)
		}
	}
}

// Spec § 6.4.
func TestABrokenTargetPostsNothingUntilFixedThenCatchesUp(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	rec := f.cardRecurrence(t, 10, 3990, 1)
	if err := f.ledger.ArchiveAccount(ctx, f.sp, f.card.ID, cardRecNow); err != nil {
		t.Fatal(err)
	}
	job := f.jobs()
	d1, n1 := at(2026, time.April, 12)
	job.Materialise(ctx, true, d1, n1)
	got, cursor, _ := f.recs.GetWithCursor(ctx, f.sp, rec.ID)
	if got.NeedsAttention != finance.AttentionCardArchived || !cursor.IsZero() || balance(t, f.ledger, f.sp, "subs") != 0 {
		t.Fatalf("archived card: %+v cursor %s", got, cursor)
	}
	other, err := f.cards.CreateCard(ctx, f.sp, "Master", finance.Card{ClosingDay: 25, DueDay: 5, PayingAccountID: "bank"}, cardRecNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{CardID: &other.ID}, n1); err != nil {
		t.Fatal(err)
	}
	job.Materialise(ctx, true, d1, n1)
	if got := balance(t, f.ledger, f.sp, "subs"); got != 2*3990 {
		t.Fatalf("subs = %d, want March 10 and April 10 posted on the new card", got)
	}
	got, cursor, _ = f.recs.GetWithCursor(ctx, f.sp, rec.ID)
	if got.NeedsAttention != "" || cursor != d1 {
		t.Fatalf("after the fix: %+v cursor %s", got, cursor)
	}

	// f.card is archived now: this one is on the working card.
	cat, err := f.recs.Create(ctx, f.sp, finance.Recurrence{Direction: finance.Payable, Amount: 1000, CategoryID: "subs", CardID: other.ID, Installments: 1,
		Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: 15}, Start: brcal.New(2026, time.March, 1), Adjust: finance.AdjustNone}},
		repositories.PostMeta{}, n1)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.ledger.ArchiveAccount(ctx, f.sp, "subs", n1); err != nil {
		t.Fatal(err)
	}
	d2, n2 := at(2026, time.April, 16)
	job.Materialise(ctx, true, d2, n2)
	if got, _ := f.recs.Get(ctx, f.sp, cat.ID); got.NeedsAttention != finance.AttentionCategoryArchived {
		t.Fatalf("archived category: needs_attention = %q", got.NeedsAttention)
	}
	food := "food"
	if err := f.recs.Update(ctx, f.sp, cat.ID, repositories.RecurrencePatch{CategoryID: &food}, n2); err != nil {
		t.Fatal(err)
	}
	job.Materialise(ctx, true, d2, n2)
	if got := balance(t, f.ledger, f.sp, "food"); got != 2*1000 {
		t.Fatalf("food = %d, want March 15 and April 15 once the category is fixed", got)
	}
}

// Review Focus 2.
func TestSwitchingTargetNeverPostsAnOccurrenceTwice(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	rec, err := f.recs.Create(ctx, f.sp, finance.Recurrence{Direction: finance.Payable, Amount: 5000, CategoryID: "subs", AccountID: "bank",
		Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: 10}, Start: brcal.New(2026, time.March, 1), Adjust: finance.AdjustNone}},
		repositories.PostMeta{}, cardRecNow)
	if err != nil {
		t.Fatal(err)
	}
	job := f.jobs()
	d1, n1 := at(2026, time.March, 20)
	job.Materialise(ctx, true, d1, n1) // bills for Mar 10 and Apr 10 (horizon: end of April)
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{CardID: &f.card.ID}, n1); err != nil {
		t.Fatal(err)
	}
	d2, n2 := at(2026, time.May, 10)
	job.Materialise(ctx, true, d2, n2)
	if got := balance(t, f.ledger, f.sp, "subs"); got != 3*5000 {
		t.Fatalf("subs = %d, want Mar and Apr as bills and May as a purchase, each once", got)
	}
	bank := "bank"
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{AccountID: &bank}, n2); err != nil {
		t.Fatal(err)
	}
	job.Materialise(ctx, true, d2, n2) // bills from May 11 on: June 10
	if got := balance(t, f.ledger, f.sp, "subs"); got != 4*5000 {
		t.Fatalf("subs = %d, want June added as a bill and May not again", got)
	}
}

// Review Focus 3, against the tables.
func TestARecurrenceOlderThanTheCardSkipsWhatCameBefore(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	if _, err := f.recs.Create(ctx, f.sp, finance.Recurrence{Direction: finance.Payable, Amount: 1000, CategoryID: "subs", CardID: f.card.ID, Installments: 1,
		Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: 5}, Start: brcal.New(2026, time.January, 1), Adjust: finance.AdjustNone}},
		repositories.PostMeta{}, cardRecNow); err != nil {
		t.Fatal(err)
	}
	d, n := at(2026, time.March, 6)
	f.jobs().Materialise(ctx, true, d, n)
	if got := balance(t, f.ledger, f.sp, "subs"); got != 1000 {
		t.Fatalf("subs = %d, want only March 5 (the card was added in March)", got)
	}
}
