package billing

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The catalogue the console offers is every EventType constant in this
// package except ping. Read from the source, so a new event cannot be added
// without either appearing in the console or failing here.
func TestTheWebhookEventListIsEveryEventConstant(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	var declared []EventType
	{
		for _, f := range files {
			ast.Inspect(f, func(n ast.Node) bool {
				spec, ok := n.(*ast.ValueSpec)
				if !ok {
					return true
				}
				typ, ok := spec.Type.(*ast.Ident)
				if !ok || typ.Name != "EventType" {
					return true
				}
				for _, v := range spec.Values {
					if lit, ok := v.(*ast.BasicLit); ok {
						s, _ := strconv.Unquote(lit.Value)
						if EventType(s) != EventPing {
							declared = append(declared, EventType(s))
						}
					}
				}
				return true
			})
		}
	}
	slices.Sort(declared)
	if got := WebhookEventTypes(); !slices.Equal(got, declared) {
		t.Fatalf("WebhookEventTypes() = %v\ndeclared %v", got, declared)
	}
	if IsWebhookEventType(EventPing) || !IsWebhookEventType(EventInvoicePaid) {
		t.Fatal("IsWebhookEventType disagrees with the list")
	}
}
