package services

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func validOneOff() OneOffInput {
	return OneOffInput{CustomerID: "cus_1", Description: "Pacote", AmountCents: 500, Reference: "snd_abc"}
}

func TestAOneOffInvoiceMayCarryAPayerMessageAndAPixLifetimeWithinTheirBounds(t *testing.T) {
	ok := validOneOff()
	ok.PayerMessage, ok.PixExpiresInSeconds = "Pacote de 10.000 fichas", 3600
	if err := ok.validate(); err != nil {
		t.Fatalf("a message and a one-hour Pix: %v", err)
	}
	for name, mutate := range map[string]func(*OneOffInput){
		"a message over 140 characters": func(in *OneOffInput) { in.PayerMessage = strings.Repeat("x", 141) },
		"a Pix lifetime under a minute": func(in *OneOffInput) { in.PixExpiresInSeconds = 59 },
		"a Pix lifetime over a day":     func(in *OneOffInput) { in.PixExpiresInSeconds = 86401 },
		"a negative lifetime":           func(in *OneOffInput) { in.PixExpiresInSeconds = -1 },
	} {
		in := validOneOff()
		mutate(&in)
		if err := in.validate(); !errors.Is(err, ErrOneOffInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestTheBuiltInvoiceKeepsWhatTheCallerAsked(t *testing.T) {
	in := validOneOff()
	in.PayerMessage, in.PixExpiresInSeconds = "  Depósito na carteira ", 1800
	inv, _ := (&OneOffInvoices{}).build(in, time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC))
	if inv.PayerMessage != "Depósito na carteira" || inv.PixExpiresInSeconds != 1800 {
		t.Fatalf("%q %d", inv.PayerMessage, inv.PixExpiresInSeconds)
	}
}
