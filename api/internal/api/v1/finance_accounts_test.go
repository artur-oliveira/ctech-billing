package v1

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAccountDTOSystemKey(t *testing.T) {
	b, _ := json.Marshal(accountDTO{ID: "cat-salario", Name: "Salário", SystemKey: "salary"})
	if !strings.Contains(string(b), `"system_key":"salary"`) {
		t.Fatalf("missing system_key: %s", b)
	}
	b, _ = json.Marshal(accountDTO{ID: "mine", Name: "x"})
	if strings.Contains(string(b), "system_key") {
		t.Fatalf("user account must omit system_key: %s", b)
	}
}
