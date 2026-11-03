package statement

import (
	"errors"
	"strings"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

const interHeader = "\xEF\xBB\xBF\"Data\",\"Lançamento\",\"Categoria\",\"Tipo\",\"Valor\"\n"

func interCSV(rows ...string) []byte { return []byte(interHeader + strings.Join(rows, "\n") + "\n") }

func row(date, desc, cat, tipo, value string) string {
	return `"` + date + `","` + desc + `","` + cat + `","` + tipo + `","` + value + `"`
}

func TestInterCardParsesAmountsAndDates(t *testing.T) {
	lines, rej, err := ParseInterCard(interCSV(
		row("29/10/2026", "99APP        99App     BRA", "TRANSPORTE", "Compra à vista", "R$ 6,50"),
		row("06/10/2026", "PAGAMENTO ON LINE", "OUTROS", "Compra à vista", "-R$ 5.052,57"),
		row("26/10/2026", "IFD CD RESTAURANTES", "SERVICOS", "Compra à vista", "-R$ 5,00"),
	))
	if err != nil || len(rej) != 0 || len(lines) != 3 {
		t.Fatalf("lines=%d rej=%v err=%v", len(lines), rej, err)
	}
	if l := lines[0]; l.Amount != 650 || l.Date != brcal.New(2026, 10, 29) || l.K != 0 || l.N != 0 || l.Remaining() != 1 || l.BankCategory != "TRANSPORTE" || l.Description != "99APP 99App BRA" {
		t.Fatalf("line 0 = %+v", l)
	}
	if lines[1].Amount != -505257 || !lines[1].Payment {
		t.Fatalf("payment = %+v", lines[1])
	}
	if lines[2].Amount != -500 || lines[2].Payment {
		t.Fatalf("refund = %+v", lines[2])
	}
}

func TestInterCardInstallment(t *testing.T) {
	lines, _, err := ParseInterCard(interCSV(row("29/08/2026", "SHEIN COM", "VESTUARIO", "Parcela 3/12", "R$ 23,83")))
	if err != nil || len(lines) != 1 {
		t.Fatal(lines, err)
	}
	if l := lines[0]; l.K != 3 || l.N != 12 || l.Remaining() != 10 || l.Amount != 2383 {
		t.Fatalf("installment = %+v", l)
	}
}

func TestInterCardRejectsBadRowsAndKeepsTheRest(t *testing.T) {
	lines, rej, err := ParseInterCard(interCSV(
		row("31/02/2026", "A", "X", "Compra à vista", "R$ 1,00"),
		row("01/10/2026", "B", "X", "Compra à vista", "abc"),
		row("01/10/2026", "C", "X", "Compra à vista", "R$ 0,00"),
		row("01/10/2026", "D", "X", "Parcela 0/12", "R$ 1,00"),
		row("01/10/2026", "OK", "X", "Compra à vista", "R$ 1,00"),
	))
	if err != nil || len(lines) != 1 || lines[0].Description != "OK" {
		t.Fatalf("lines=%+v err=%v", lines, err)
	}
	want := []Rejected{{2, ReasonDate}, {3, ReasonAmount}, {4, ReasonZero}, {5, ReasonInstallment}}
	if len(rej) != len(want) {
		t.Fatalf("rejected = %+v", rej)
	}
	for i := range want {
		if rej[i] != want[i] {
			t.Errorf("rejected[%d] = %+v, want %+v", i, rej[i], want[i])
		}
	}
}

func TestInterCardWrongFileIsUnreadable(t *testing.T) {
	if _, _, err := ParseInterCard([]byte("Data;Historico;Valor\n01/10/2026;X;1,00\n")); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("wrong header: %v", err)
	}
	if _, _, err := ParseInterCard([]byte(interHeader)); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty body: %v", err)
	}
	rows := make([]string, MaxLines+1)
	for i := range rows {
		rows[i] = row("01/10/2026", "X", "X", "Compra à vista", "R$ 1,00")
	}
	if _, _, err := ParseInterCard(interCSV(rows...)); !errors.Is(err, ErrTooMany) {
		t.Fatalf("too many: %v", err)
	}
	if _, _, err := ParseInterCard(make([]byte, MaxFileBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("too large: %v", err)
	}
}

func TestInterCardReadsWindows1252(t *testing.T) {
	// The whole file in Windows-1252, header included.
	b := []byte("\"Data\",\"Lan\xe7amento\",\"Categoria\",\"Tipo\",\"Valor\"\n\"01/10/2026\",\"CAF\xc9\",\"X\",\"Compra \xe0 vista\",\"R$ 1,00\"\n")
	lines, _, err := ParseInterCard(b)
	if err != nil || len(lines) != 1 || lines[0].Description != "CAFÉ" {
		t.Fatalf("lines=%+v err=%v", lines, err)
	}
}
