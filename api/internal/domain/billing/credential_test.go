package billing

import "testing"

func TestACredentialWithoutAConnectorWasProvisioned(t *testing.T) {
	if !(&APICredential{ClientID: "dfe-billing"}).Provisioned() {
		t.Fatal("a seeded credential is not provisioned")
	}
	if (&APICredential{ClientID: "c", ConnectedBy: "usr_1"}).Provisioned() {
		t.Fatal("a console credential is provisioned")
	}
}
