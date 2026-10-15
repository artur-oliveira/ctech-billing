package realtime

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

type docRow struct {
	entity string
	types  []string
}

// eventsDoc reads the catalogue table of docs/realtime/events.md, the one list
// the UI's contract test reads too.
func eventsDoc(t *testing.T) []docRow {
	t.Helper()
	raw, err := os.ReadFile("../../../docs/realtime/events.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	begin := strings.Index(text, "<!-- events:begin -->")
	end := strings.Index(text, "<!-- events:end -->")
	if begin < 0 || end < begin {
		t.Fatal("events.md lost its table markers")
	}
	var rows []docRow
	for _, line := range strings.Split(text[begin:end], "\n") {
		if !strings.HasPrefix(line, "|") || strings.HasPrefix(line, "|---") || strings.HasPrefix(line, "| entity") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "| "), "|")
		row := docRow{entity: strings.TrimSpace(cells[0])}
		for _, ty := range strings.Split(cells[1], ",") {
			row.types = append(row.types, strings.TrimSpace(ty))
		}
		rows = append(rows, row)
	}
	return rows
}

// entityConstants are the Entity* string constants declared in emit.go, read
// from the source so that a new one cannot be forgotten by Entities().
func entityConstants(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "emit.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Entity") {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok {
					continue
				}
				value, _ := strconv.Unquote(lit.Value)
				out = append(out, value)
			}
		}
	}
	return out
}

func TestEventsDocListsEveryEntityTheEmitterCanProduce(t *testing.T) {
	var documented []string
	for _, row := range eventsDoc(t) {
		documented = append(documented, row.entity)
	}
	want := entityConstants(t)
	slices.Sort(documented)
	slices.Sort(want)
	if !slices.Equal(documented, want) {
		t.Fatalf("events.md entities %v, emitter entities %v: a new emission site needs a row", documented, want)
	}
	listed := Entities()
	slices.Sort(listed)
	if !slices.Equal(listed, want) {
		t.Fatalf("Entities() %v is not every Entity constant %v", listed, want)
	}
}

func TestEventsDocListsEveryTypeTheEmitterCanProduce(t *testing.T) {
	var documented []string
	for _, row := range eventsDoc(t) {
		documented = append(documented, row.types...)
	}
	var want []string
	for _, ty := range billing.WebhookEventTypes() {
		want = append(want, string(ty))
	}
	want = append(want, RealtimeOnlyTypes()...)
	slices.Sort(documented)
	slices.Sort(want)
	if !slices.Equal(documented, want) {
		t.Fatalf("events.md types %v, emitted types %v", documented, want)
	}
}
