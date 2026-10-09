package limits

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// The UI enforces the same limits from ui/src/lib/limits.json. Two copies of a
// limit drift, and the drift surfaces as a form that accepts what the API then
// refuses; this test makes the JSON and these constants one definition.
func TestTheUIReadsTheseLimits(t *testing.T) {
	raw, err := os.ReadFile("../../../ui/src/lib/limits.json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"maxAmountCents":     float64(MaxAmountCents),
		"minDate":            MinDate.String(),
		"maxFutureYears":     float64(MaxFutureYears),
		"maxRecurrenceYears": float64(MaxRecurrenceYears),
		"text": map[string]any{
			"accountName": float64(AccountName), "description": float64(Description), "memo": float64(Memo),
			"productName": float64(ProductName), "ownerKey": float64(OwnerKey), "customerName": float64(CustomerName),
			"legalName": float64(LegalName), "address": float64(Address), "email": float64(Email),
			"reason": float64(Reason), "externalRef": float64(ExternalRef), "taxID": float64(TaxID),
		},
		"maxInstallments":      float64(MaxInstallments),
		"maxNetDays":           float64(MaxNetDays),
		"maxSubscriptionItems": float64(MaxSubscriptionItems),
		"maxUsageQuantity":     float64(MaxUsageQuantity),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ui/src/lib/limits.json = %v\nwant %v", got, want)
	}
}
