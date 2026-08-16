// Package billing holds the pure billing domain: money arithmetic, metadata
// rules, billing cycles, proration, and the entity state machines.
//
// Nothing in this package performs I/O, reads a clock, or knows about DynamoDB,
// HTTP or the wallet. That is deliberate (PLAN.md Phase 1): the arithmetic and
// the state transitions are where a silent bug costs real money, and they are
// only cheap to test while they have no dependencies.
package billing

import (
	"fmt"
	"strings"
)

// Cents is an amount in centavos. Money is never a float in this system:
// binary floating point cannot represent 0,10 exactly, and a rounding drift in a
// recurring charge compounds every cycle.
type Cents int64

// MaxChargeCents mirrors the per-charge ceiling of ADR 0004 (R$ 10.000,00).
//
// The ceiling is **enforced in the wallet**, server-side, which is the only
// enforcement that counts — this constant exists so billing can reject a price
// at creation time with a clear message instead of letting the merchant discover
// it as a 422 on the customer's first charge. Never treat it as the control.
//
// It is a mirror, so it is only right while it agrees with `max_charge_cents`
// for the billing client in ctech-wallet's `/ctech-wallet/{env}/m2m-clients`
// parameter. Wallet's own default is still R$ 1.000,00
// (`services.DefaultMaxChargeCents`); raising this constant without raising that
// value moves nothing except which side reports the refusal.
//
// R$ 10.000,00 is the same figure as wallet's `DefaultMaxDeposit`, deliberately:
// a charge larger than a customer can fund in one PIX is a charge that cannot
// be paid, so there is no useful ceiling above that one.
const MaxChargeCents Cents = 1_000_000

// Currency is an ISO 4217 code. Only BRL exists today; the field is kept so that
// adding a currency later is a validation change and not a schema migration
// (assessment § 13: multi-currency is explicitly not being built).
const CurrencyBRL = "BRL"

// String renders the amount as Brazilian currency, e.g. "R$ 1.234,56".
func (c Cents) String() string {
	neg := c < 0
	v := int64(c)
	if neg {
		v = -v
	}
	units, frac := v/100, v%100

	// Group the integer part in threes with "." as the separator.
	digits := fmt.Sprintf("%d", units)
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}

	sign := ""
	if neg {
		sign = "-"
	}
	return fmt.Sprintf("%sR$ %s,%02d", sign, b.String(), frac)
}

// MulDiv returns amount * num / den, rounded half away from zero.
//
// This is the **only** place billing rounds money. Every proration, discount and
// tiered calculation goes through it, so the rounding policy is one decision in
// one place rather than a habit that varies by author (assessment § 13:
// "definir política de arredondamento de pró-rata em um lugar só").
//
// Half away from zero is chosen over banker's rounding because it is the rule a
// customer reconstructs with a calculator when they dispute a proration, and
// because symmetry means crediting the same fraction that was charged returns
// exactly the amount charged.
//
// Overflow: amounts are bounded by MaxChargeCents and num by the days in a
// billing period, so int64 has several orders of magnitude of headroom. A caller
// passing an unbounded num is outside the contract.
func MulDiv(amount Cents, num, den int64) Cents {
	if den == 0 {
		panic("billing: MulDiv by zero denominator")
	}
	n := int64(amount) * num
	negative := (n < 0) != (den < 0)
	a, d := absInt64(n), absInt64(den)
	q := (a + d/2) / d
	if negative {
		return Cents(-q)
	}
	return Cents(q)
}

func absInt64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
