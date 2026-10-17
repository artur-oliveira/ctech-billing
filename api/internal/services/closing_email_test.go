package services

import (
	"context"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/accountclient"
	"gopkg.aoctech.app/billing/api/internal/email"
)

type fakeContacts struct{ c accountclient.Contact }

func (f fakeContacts) Contact(context.Context, string) (accountclient.Contact, error) {
	return f.c, nil
}

type captureSend struct {
	to string
	n  email.ClosureNotice
}

func (c *captureSend) SendClosureNotice(_ context.Context, to string, n email.ClosureNotice) error {
	c.to, c.n = to, n
	return nil
}

func TestClosureEmailsGoToTheAccountsVerifiedAddressWithConsoleLinks(t *testing.T) {
	send := &captureSend{}
	m := NewClosureEmails(fakeContacts{accountclient.Contact{Email: "owner@example.com", FirstName: "Ana", EmailVerified: true}}, send, "https://billing.aoctech.app/")
	if err := m.SendClosureConfirm(context.Background(), "usr_1", "clo_1", "tok en"); err != nil {
		t.Fatal(err)
	}
	if send.to != "owner@example.com" || send.n.FirstName != "Ana" || send.n.Kind != "confirm" ||
		send.n.Link != "https://billing.aoctech.app/closure/confirm?request=clo_1&token=tok+en" {
		t.Fatalf("sent %q %+v", send.to, send.n)
	}
	if err := m.SendClosureScheduled(context.Background(), "usr_1", "clo_1", "c", time.Now()); err != nil || !strings.Contains(send.n.Link, "/closure/cancel?") {
		t.Fatalf("scheduled link %q %v", send.n.Link, err)
	}
}

func TestClosureEmailsRefuseAnUnverifiedAddress(t *testing.T) {
	send := &captureSend{}
	m := NewClosureEmails(fakeContacts{accountclient.Contact{Email: "x@example.com", EmailVerified: false}}, send, "https://b")
	if err := m.SendClosureConfirm(context.Background(), "usr_1", "clo_1", "t"); err == nil {
		t.Fatal("an unverified address must not receive a closure link")
	}
	if send.to != "" {
		t.Fatal("something was sent")
	}
}
