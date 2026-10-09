package v1

import (
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/limits"
)

func fields(c *checks) []string {
	out := make([]string, len(c.errs))
	for i, e := range c.errs {
		out[i] = e.Field
	}
	return out
}

func TestTextChecks(t *testing.T) {
	c := &checks{}
	c.text("ok", "Aluguel", true, 10)
	c.text("blank", "   ", true, 10)
	c.text("long", strings.Repeat("é", 11), false, 10) // runes, not bytes
	c.text("tenRunes", strings.Repeat("é", 10), false, 10)
	c.text("control", "a\x00b", false, 10)
	c.text("newline", "a\nb", false, 10)
	c.text("optional", "", false, 10)
	if got := strings.Join(fields(c), ","); got != "blank,long,control,newline" {
		t.Fatalf("errors on %s", got)
	}
}

func TestAmountChecks(t *testing.T) {
	c := &checks{}
	c.amount("zero", 0)
	c.amount("neg", -1)
	c.amount("max", limits.MaxAmountCents)
	c.amount("over", limits.MaxAmountCents+1)
	c.signedAmount("szero", 0)
	c.signedAmount("sneg", -limits.MaxAmountCents)
	c.signedAmount("sover", -limits.MaxAmountCents-1)
	c.price("free", 0)
	c.price("pneg", -1)
	if got := strings.Join(fields(c), ","); got != "zero,neg,over,szero,sover,pneg" {
		t.Fatalf("errors on %s", got)
	}
}

func TestDateChecks(t *testing.T) {
	today := brcal.New(2026, time.October, 8)
	c := &checks{}
	c.date("old", brcal.New(1999, time.December, 31), limits.MinDate, today)
	c.date("first", limits.MinDate, limits.MinDate, today)
	c.date("future", today.AddDays(1), limits.MinDate, today)
	c.date("zero", brcal.Date{}, limits.MinDate, today)
	c.date("far", brcal.New(2036, time.October, 9), limits.MinDate, limits.MaxDate(today))
	if got := strings.Join(fields(c), ","); got != "old,future,zero,far" {
		t.Fatalf("errors on %s", got)
	}
}

func TestIDEmailAndTaxIDChecks(t *testing.T) {
	c := &checks{}
	c.id("ok", "01M4F5EQ6V4YGJJPT1PQ7HYJBG", true)
	c.id("hash", "a#b", true)
	c.id("missing", "", true)
	c.email("email", "pessoa@exemplo.com.br", true)
	c.email("bademail", "pessoa@", true)
	c.email("longemail", strings.Repeat("a", 250)+"@x.io", true)
	c.taxID("cpf", "529.982.247-25", true, true)
	c.taxID("badcpf", "529.982.247-24", true, true)
	c.taxID("cnpj", "11.222.333/0001-81", true, true)
	c.taxID("letters", "12.ABC.345/01DE-35", true, true) // alphanumeric CNPJ (2026)
	c.taxID("garbage", "abc", true, false)
	c.taxID("shapeonly", "000.000.000-00", true, false) // integrators' test data: shape checked, digits not
	if got := strings.Join(fields(c), ","); got != "hash,missing,bademail,longemail,badcpf,garbage" {
		t.Fatalf("errors on %s", got)
	}
}

func TestFieldErrorsCarryCodeAndParams(t *testing.T) {
	c := &checks{}
	c.text("name", strings.Repeat("a", 11), false, 10)
	c.text("req", "", true, 10)
	c.date("d", brcal.New(1999, time.December, 31), limits.MinDate, brcal.New(2026, time.October, 8))
	c.id("id", "a b", true)
	c.email("e", "nope", true)
	want := []struct {
		code string
		key  string
	}{{"too_long", "max"}, {"required", ""}, {"date_too_early", "min"}, {"invalid_id", ""}, {"invalid_email", ""}}
	if len(c.errs) != len(want) {
		t.Fatalf("got %d errors: %+v", len(c.errs), c.errs)
	}
	for i, w := range want {
		if c.errs[i].Code != w.code {
			t.Errorf("error %d code = %q, want %q", i, c.errs[i].Code, w.code)
		}
		if w.key != "" {
			if _, ok := c.errs[i].Params[w.key]; !ok {
				t.Errorf("error %d lacks param %q: %+v", i, w.key, c.errs[i].Params)
			}
		}
		if c.errs[i].Message == "" {
			t.Errorf("error %d has no fallback message", i)
		}
	}
	if c.errs[0].Params["max"] != 10 {
		t.Errorf("max param = %v", c.errs[0].Params["max"])
	}
}
