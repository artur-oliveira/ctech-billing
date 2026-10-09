package statement

import (
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

var semicolonBR = Mapping{Delimiter: ";", Decimal: ",", DateFormat: DateDMY, SkipRows: 1, Date: 1, Description: 2, Amount: 3}

func TestParseCSVWithASignedColumn(t *testing.T) {
	raw := "Data;Histórico;Valor\r\n" +
		"05/03/2026;PIX ENVIADO  Fulano;-1.250,00\r\n" +
		"06/03/2026 10:22;\"Salário; março\";5.000,00\r\n" +
		";;\r\n" +
		"06/03/2026;SALDO DO DIA;3.750,00\r\n" +
		"31/02/2026;data errada;-1,00\r\n" +
		"07/03/26;ano curto;-0,99\r\n"
	p, err := ParseCSV([]byte(raw), semicolonBR)
	if err != nil {
		t.Fatal(err)
	}
	want := []Line{
		{Date: brcal.New(2026, time.March, 5), Amount: -125000, Description: "PIX ENVIADO Fulano"},
		{Date: brcal.New(2026, time.March, 6), Amount: 500000, Description: "Salário; março"},
		{Date: brcal.New(2026, time.March, 7), Amount: -99, Description: "ano curto"},
	}
	if len(p.Lines) != len(want) {
		t.Fatalf("lines = %+v", p.Lines)
	}
	for i := range want {
		if p.Lines[i] != want[i] {
			t.Errorf("line %d = %+v, want %+v", i, p.Lines[i], want[i])
		}
	}
	// Rows are counted as the file shows them, header and blank row included.
	if len(p.Rejected) != 2 || p.Rejected[0] != (Rejected{5, ReasonBalanceRow}) || p.Rejected[1] != (Rejected{6, ReasonDate}) {
		t.Fatalf("rejected = %+v", p.Rejected)
	}
}

func TestParseCSVWithCreditAndDebitColumns(t *testing.T) {
	m := Mapping{Delimiter: ",", Decimal: ".", DateFormat: DateYMD, Date: 1, Description: 2, Amount: 3, Debit: 4}
	raw := "2026-03-01,Deposit,100.00,\n2026-03-02,Rent,,1200.00\n2026-03-03,Fee written negative,,-3.50\n2026-03-04,Both,1.00,1.00\n"
	p, err := ParseCSV([]byte(raw), m)
	if err != nil {
		t.Fatal(err)
	}
	got := []billing.Cents{}
	for _, l := range p.Lines {
		got = append(got, l.Amount)
	}
	if len(got) != 3 || got[0] != 10000 || got[1] != -120000 || got[2] != -350 {
		t.Fatalf("amounts = %v", got)
	}
	if len(p.Rejected) != 1 || p.Rejected[0].Reason != ReasonAmount {
		t.Fatalf("rejected = %+v", p.Rejected)
	}
}

func TestParseCSVReadsWindows1252AndADifferentDateOrder(t *testing.T) {
	m := Mapping{Delimiter: "\t", Decimal: ".", DateFormat: DateMDY, Date: 2, Description: 1, Amount: 3}
	raw := []byte("Padaria S\xe3o Jo\xe3o\t03/15/2026\t-12.30\n")
	p, err := ParseCSV(raw, m)
	if err != nil {
		t.Fatal(err)
	}
	if p.Lines[0].Description != "Padaria São João" || p.Lines[0].Date != brcal.New(2026, time.March, 15) {
		t.Fatalf("got %+v", p.Lines[0])
	}
}

func TestParseCSVRefusals(t *testing.T) {
	if _, err := ParseCSV([]byte("Data;Valor\n"), semicolonBR); !errors.Is(err, ErrEmpty) {
		t.Errorf("header only: %v", err)
	}
	if _, err := ParseCSV([]byte("01/03/2026;x;1\n"), Mapping{}); !errors.Is(err, ErrInvalidMapping) {
		t.Errorf("zero mapping: %v", err)
	}
}

func TestMappingValidate(t *testing.T) {
	if err := semicolonBR.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]Mapping{
		"pipe":           {Delimiter: "|", Decimal: ",", DateFormat: DateDMY, Date: 1, Description: 2, Amount: 3},
		"comma both":     {Delimiter: ",", Decimal: ",", DateFormat: DateDMY, Date: 1, Description: 2, Amount: 3},
		"format":         {Delimiter: ";", Decimal: ",", DateFormat: "dd.mm", Date: 1, Description: 2, Amount: 3},
		"no amount":      {Delimiter: ";", Decimal: ",", DateFormat: DateDMY, Date: 1, Description: 2},
		"same column":    {Delimiter: ";", Decimal: ",", DateFormat: DateDMY, Date: 1, Description: 1, Amount: 3},
		"debit = amount": {Delimiter: ";", Decimal: ",", DateFormat: DateDMY, Date: 1, Description: 2, Amount: 3, Debit: 3},
		"column 51":      {Delimiter: ";", Decimal: ",", DateFormat: DateDMY, Date: 51, Description: 2, Amount: 3},
		"skip 21":        {Delimiter: ";", Decimal: ",", DateFormat: DateDMY, SkipRows: 21, Date: 1, Description: 2, Amount: 3},
	} {
		if err := m.Validate(); !errors.Is(err, ErrInvalidMapping) {
			t.Errorf("%s: %v, want ErrInvalidMapping", name, err)
		}
	}
}
