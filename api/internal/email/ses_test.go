package email

import (
	"context"
	"strings"
	"testing"
)

type sent struct{ to, subject, body string }

type fakeTransport struct{ out []sent }

func (f *fakeTransport) Send(_ context.Context, to, subject, body string) error {
	f.out = append(f.out, sent{to, subject, body})
	return nil
}

func TestTheReceivedNoticeNamesTheOrganizationAndEscapesWhatTheApplicantTyped(t *testing.T) {
	tx := &fakeTransport{}
	c := &Client{tx: tx}
	err := c.SendApplicationReceived(context.Background(), "time@ctech.example", ApplicationReceived{
		ApplicationID: "apl_1", OrganizationName: "Padaria <b>Bom</b> Dia", CompanyLegalName: "Padaria LTDA",
		Description: "<script>x</script> pão", Website: "https://padaria.example", ContactEmail: "c@padaria.example",
	})
	if err != nil || len(tx.out) != 1 {
		t.Fatalf("%v %+v", err, tx.out)
	}
	m := tx.out[0]
	if m.to != "time@ctech.example" || !strings.Contains(m.subject, "Padaria <b>Bom</b> Dia") {
		t.Fatalf("to/subject = %q / %q", m.to, m.subject)
	}
	if strings.Contains(m.body, "<script>") || strings.Contains(m.body, "<b>Bom</b>") || !strings.Contains(m.body, "apl_1") {
		t.Fatalf("body not escaped or missing the id: %s", m.body)
	}
}

func TestTheDecisionSaysWhichWayAndWhy(t *testing.T) {
	tx := &fakeTransport{}
	c := &Client{tx: tx}
	_ = c.SendApplicationDecided(context.Background(), "c@padaria.example", ApplicationDecided{OrganizationName: "Padaria", Approved: true})
	_ = c.SendApplicationDecided(context.Background(), "c@padaria.example", ApplicationDecided{OrganizationName: "Padaria", Reason: "Site <fora> do ar"})
	if !strings.Contains(tx.out[0].subject, "aprovado") || strings.Contains(tx.out[0].body, "Motivo") {
		t.Fatalf("approval = %+v", tx.out[0])
	}
	if !strings.Contains(tx.out[1].subject, "não foi aprovado") || !strings.Contains(tx.out[1].body, "Site &lt;fora&gt; do ar") {
		t.Fatalf("rejection = %+v", tx.out[1])
	}
}

func TestNoRecipientIsAnError(t *testing.T) {
	c := &Client{tx: &fakeTransport{}}
	if c.SendApplicationDecided(context.Background(), "", ApplicationDecided{}) == nil {
		t.Fatal("sent to nobody")
	}
}
