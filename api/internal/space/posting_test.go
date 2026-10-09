package space

import (
	"errors"
	"testing"
)

func TestPostingSpacesCarryTheInvoiceModeAndOnlyThePostingVerbs(t *testing.T) {
	org := "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
	issuer, err := ForInvoiceIssuer(org, false)
	if err != nil {
		t.Fatal(err)
	}
	if issuer.PK() != org+"#test" || issuer.Personal() {
		t.Fatalf("issuer = %q personal=%v", issuer.PK(), issuer.Personal())
	}
	payer, err := ForInvoicePayer("sub-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if payer.PK() != "USER#sub-1#live" || !payer.Personal() {
		t.Fatalf("payer = %q", payer.PK())
	}
	for _, sp := range []ResolvedSpace{issuer, payer} {
		if !sp.Can(Read|Write|Settle) || sp.Can(Import) || sp.Can(Configure) {
			t.Errorf("%s verbs = %v", sp.PK(), sp.Verbs().Names())
		}
	}
}

func TestPostingSpacesRefuseKeyShapedIDs(t *testing.T) {
	for _, id := range []string{"ctech", "", "USER#x", "0190A1B2-C3D4-7E5F-8A9B-0C1D2E3F4A5B"} {
		if _, err := ForInvoiceIssuer(id, true); !errors.Is(err, ErrSpaceNotFound) {
			t.Errorf("ForInvoiceIssuer(%q) err = %v", id, err)
		}
	}
	for _, sub := range []string{"", "a#b"} {
		if _, err := ForInvoicePayer(sub, true); !errors.Is(err, ErrInvalidSubject) {
			t.Errorf("ForInvoicePayer(%q) err = %v", sub, err)
		}
	}
	if !IsOrganizationID("0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b") || IsOrganizationID("ctech") {
		t.Error("IsOrganizationID disagrees with the selector's rule")
	}
}
