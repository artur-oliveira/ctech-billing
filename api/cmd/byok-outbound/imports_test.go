package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestOnlyTheLambdaImportsInterAndTheSealer(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", `{{.ImportPath}} {{join .Deps " "}}`, "gopkg.aoctech.app/billing/api/...").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pkg, deps, _ := strings.Cut(line, " ")
		if strings.HasPrefix(pkg, "gopkg.aoctech.app/billing/api/cmd/byok-outbound") || pkg == "gopkg.aoctech.app/billing/api/internal/byokseal" {
			continue
		}
		for _, banned := range []string{"gopkg.aoctech.app/api-commons/inter", "gopkg.aoctech.app/billing/api/internal/byokseal"} {
			for _, d := range strings.Fields(deps) {
				if d == banned || strings.HasPrefix(d, banned+"/") {
					t.Errorf("%s imports %s", pkg, d)
				}
			}
		}
	}
}
