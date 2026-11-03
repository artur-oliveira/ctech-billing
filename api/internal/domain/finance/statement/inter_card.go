package statement

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// FormatInterCard is the format of an import made from the Inter credit card CSV.
const FormatInterCard Format = "inter_csv"

// ReasonInstallment is a "Parcela k/n" the card statement could not read.
const ReasonInstallment = "invalid_installment"

// maxCardInstallments bounds n of "Parcela k/n" (finance.MaxInstallments is 99
// at most; a bank never prints more).
const maxCardInstallments = 99

// CardLine is one line of a bank's credit card statement. Amount is from the
// card's side: a purchase is positive, a credit or the payment of the previous
// statement is negative. FITID is always empty: no card export carries one.
type CardLine struct {
	Line
	BankCategory string // the bank's own category, "SUPERMERCADO"; "" when absent
	K, N         int    // "Parcela k/n"; 0, 0 for "Compra à vista"
	Payment      bool   // the payment of the previous statement, not a purchase
}

// Remaining is how many installments are left counting this one: n−k+1 for an
// installment, 1 otherwise. It is what a person keys by hand for an old
// installment purchase, so it is what pairing compares.
func (l CardLine) Remaining() int {
	if l.N == 0 {
		return 1
	}
	return l.N - l.K + 1
}

var interCardColumns = []string{"Data", "Lançamento", "Categoria", "Tipo", "Valor"}

// ParseInterCard reads the Inter credit card statement CSV. Rows are numbered
// as the file's (the header is row 1); a row that cannot be read is rejected
// and the rest import.
func ParseInterCard(data []byte) ([]CardLine, []Rejected, error) {
	if len(data) > MaxFileBytes {
		return nil, nil, ErrTooLarge
	}
	r := csv.NewReader(strings.NewReader(decodeText(data)))
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	head, err := r.Read()
	if err != nil || len(head) < len(interCardColumns) {
		return nil, nil, fmt.Errorf("%w: not an Inter card statement", ErrUnreadable)
	}
	for i, h := range interCardColumns {
		if strings.TrimSpace(head[i]) != h {
			return nil, nil, fmt.Errorf("%w: not an Inter card statement", ErrUnreadable)
		}
	}
	var lines []CardLine
	var rejected []Rejected
	row, n := 1, 0
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrUnreadable, err)
		}
		row++
		if blank(rec) {
			continue
		}
		if n++; n > MaxLines {
			return nil, nil, ErrTooMany
		}
		l, reason := interCardLine(rec)
		if reason != "" {
			rejected = append(rejected, Rejected{Line: row, Reason: reason})
			continue
		}
		lines = append(lines, l)
	}
	if n == 0 {
		return nil, nil, ErrEmpty
	}
	return lines, rejected, nil
}

func interCardLine(rec []string) (CardLine, string) {
	date, ok := parseCSVDate(field(rec, 1), DateDMY)
	if !ok {
		return CardLine{}, ReasonDate
	}
	amount, ok := parseAmount(field(rec, 5), ',')
	if !ok {
		return CardLine{}, ReasonAmount
	}
	if amount == 0 {
		return CardLine{}, ReasonZero
	}
	l := CardLine{
		Line:         Line{Date: date, Amount: amount, Description: cleanText(field(rec, 2), maxDescription)},
		BankCategory: cleanText(field(rec, 3), 60),
	}
	l.Payment = strings.HasPrefix(strings.ToUpper(l.Description), "PAGAMENTO")
	if tipo := field(rec, 4); strings.HasPrefix(tipo, "Parcela ") {
		ks, ns, found := strings.Cut(strings.TrimPrefix(tipo, "Parcela "), "/")
		k, kerr := strconv.Atoi(ks)
		nn, nerr := strconv.Atoi(ns)
		if !found || kerr != nil || nerr != nil || k < 1 || nn < k || nn > maxCardInstallments {
			return CardLine{}, ReasonInstallment
		}
		l.K, l.N = k, nn
	}
	return l, ""
}
