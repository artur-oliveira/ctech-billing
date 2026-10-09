package space

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// ForJob builds a space from an operator's decision, not from a request. It may
// be called from cmd/ binaries and tests; no non-test file under internal/ may
// call it, under any import alias. A handler, middleware or service that did
// would build a space from request data, which is what this package prevents.
// scheduleOwnerFile is the single named exception, not a directory.
const scheduleOwnerFile = "../repositories/finance_schedule.go"

// The posting rule builds the two spaces of a paid invoice from stored data (the
// tenant's provisioned link, the customer's user id), never from a request. One
// file may do it; a handler that did would be building a space from whatever the
// request named.
const postingRuleFile = "../services/finance_invoices.go"

func TestForJobIsNotCalledFromInternal(t *testing.T) {
	// The one exception: the daily job's cross-tenant read rebuilds a space from
	// the owner in a schedule-index key (see that file's comment).
	assertCalledOnlyFrom(t, "ForJob", scheduleOwnerFile)
}

func TestPostingSpacesAreBuiltOnlyByTheInvoicePostingRule(t *testing.T) {
	assertCalledOnlyFrom(t, "ForInvoiceIssuer", postingRuleFile)
	assertCalledOnlyFrom(t, "ForInvoicePayer", postingRuleFile)
}

// assertCalledOnlyFrom fails for every non-test file under internal/, outside
// this package and the allowed files, that calls space.<name> under any import
// alias.
func assertCalledOnlyFrom(t *testing.T, name string, allowed ...string) {
	t.Helper()
	const spacePath = "gopkg.aoctech.app/billing/api/internal/space"
	err := filepath.WalkDir("..", func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		if filepath.Dir(path) == "../space" || slices.Contains(allowed, filepath.ToSlash(path)) {
			return nil
		}
		f, perr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if perr != nil {
			return perr
		}
		local := ""
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == spacePath {
				local = "space"
				if imp.Name != nil {
					local = imp.Name.Name
				}
			}
		}
		if local == "" {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == name {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == local {
					t.Errorf("%s calls space.%s; only %v may", path, name, allowed)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
