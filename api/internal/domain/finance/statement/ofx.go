package statement

import (
	"fmt"
	"html"
	"strconv"
	"strings"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// rawTxn is one <STMTTRN> as text.
type rawTxn struct {
	trnType, posted, amount, fitid, name, memo string
}

func (r *rawTxn) set(tag, value string) {
	switch tag {
	case "TRNTYPE":
		r.trnType = strings.ToUpper(value)
	case "DTPOSTED":
		r.posted = value
	case "TRNAMT":
		r.amount = value
	case "FITID":
		r.fitid = value
	case "NAME":
		r.name = value
	case "MEMO":
		r.memo = value
	}
}

// ParseOFX reads an OFX bank statement: 1.x (SGML, leaf elements with or
// without closing tags) and 2.x (XML) alike, since both are read as a stream
// of tags. A credit card statement, a file with two accounts or a currency
// other than BRL is refused whole; a transaction that cannot be read is
// Rejected and the others import.
func ParseOFX(b []byte) (Parsed, error) {
	if len(b) > MaxFileBytes {
		return Parsed{}, ErrTooLarge
	}
	text := decodeText(b)
	start := strings.Index(strings.ToUpper(text), "<OFX>")
	if start < 0 {
		return Parsed{}, fmt.Errorf("%w: no <OFX> element", ErrUnreadable)
	}
	var (
		out        Parsed
		cur        *rawTxn
		bal        *rawBalance
		statements int
		n          int
	)
	finish := func() error {
		if cur == nil {
			return nil
		}
		n++
		if n > MaxLines {
			return ErrTooMany
		}
		if l, reason := cur.line(); reason != "" {
			out.Rejected = append(out.Rejected, Rejected{Line: n, Reason: reason})
		} else {
			out.Lines = append(out.Lines, l)
		}
		cur = nil
		return nil
	}
	for pos := start; pos < len(text); {
		lt := strings.IndexByte(text[pos:], '<')
		if lt < 0 {
			break
		}
		lt += pos
		gt := strings.IndexByte(text[lt:], '>')
		if gt < 0 {
			break
		}
		gt += lt
		tag := strings.ToUpper(strings.TrimSpace(text[lt+1 : gt]))
		value := text[gt+1:]
		if next := strings.IndexByte(value, '<'); next >= 0 {
			value = value[:next]
		}
		pos = gt + 1 + len(value)
		value = strings.TrimSpace(html.UnescapeString(value))

		switch {
		case tag == "" || tag[0] == '?' || tag[0] == '!':
		case tag == "CCSTMTRS" || tag == "CREDITCARDMSGSRSV1":
			return Parsed{}, ErrCardStatement
		case tag == "STMTRS":
			if statements++; statements > 1 {
				return Parsed{}, ErrManyAccounts
			}
		case tag == "CURDEF":
			if value != "" && !strings.EqualFold(value, "BRL") {
				return Parsed{}, fmt.Errorf("%w: %s", ErrCurrency, value)
			}
		case tag == "STMTTRN":
			// A transaction left open by a truncated or sloppy exporter ends
			// where the next one begins.
			if err := finish(); err != nil {
				return Parsed{}, err
			}
			cur = &rawTxn{}
		case tag == "/STMTTRN", tag == "/BANKTRANLIST":
			if err := finish(); err != nil {
				return Parsed{}, err
			}
		// The ledger balance (UX batch 5). Its BALAMT and DTASOF are leaves
		// that AVAILBAL repeats, so they count only inside LEDGERBAL, which
		// ends at its closing tag or at the next aggregate.
		case tag == "LEDGERBAL":
			bal = &rawBalance{}
		case tag == "/LEDGERBAL", tag == "AVAILBAL", tag == "/STMTRS":
			if bal != nil {
				out.Ledger = bal.balance()
				bal = nil
			}
		case bal != nil && tag == "BALAMT":
			bal.amount = value
		case bal != nil && tag == "DTASOF":
			bal.asOf = value
		case cur != nil && tag[0] != '/':
			cur.set(tag, value)
		}
	}
	if err := finish(); err != nil {
		return Parsed{}, err
	}
	if bal != nil { // a file cut inside LEDGERBAL
		out.Ledger = bal.balance()
	}
	if n == 0 {
		return Parsed{}, ErrEmpty
	}
	return out, nil
}

// line reads one transaction, or says why it cannot be read.
func (r rawTxn) line() (Line, string) {
	date, ok := parseOFXDate(r.posted)
	if !ok {
		return Line{}, ReasonDate
	}
	amount, ok := parseAmount(r.amount, 0)
	if !ok {
		return Line{}, ReasonAmount
	}
	if amount == 0 {
		return Line{}, ReasonZero
	}
	// TRNAMT is signed by the OFX spec, and that sign is trusted — except for
	// the two generic types whose direction is their meaning: some banks write
	// every amount positive and say DEBIT or CREDIT. Other types (INT, FEE,
	// POS…) keep their sign, since interest can be charged or paid.
	switch r.trnType {
	case "DEBIT":
		if amount > 0 {
			amount = -amount
		}
	case "CREDIT":
		if amount < 0 {
			amount = -amount
		}
	}
	return Line{Date: date, Amount: amount, Description: describe(r.name, r.memo), FITID: cleanText(r.fitid, maxFITID)}, ""
}

// describe joins NAME and MEMO: banks put the transaction's kind in one and the
// counterparty in the other, in no fixed order. MEMO leads; NAME is added when
// MEMO does not already say it.
func describe(name, memo string) string {
	name, memo = cleanText(name, maxDescription), cleanText(memo, maxDescription)
	switch {
	case memo == "":
		return name
	case name == "" || strings.Contains(strings.ToUpper(memo), strings.ToUpper(name)):
		return memo
	default:
		return cleanText(memo+" - "+name, maxDescription)
	}
}

// parseOFXDate reads the date part of YYYYMMDD[HHMMSS[.XXX]][[offset:TZ]]. The
// civil day is the one the bank wrote; the time and the zone are ignored, so a
// purchase late on the 5th stays on the 5th.
func parseOFXDate(s string) (brcal.Date, bool) {
	if len(s) < 8 || !digits(s[:8]) {
		return brcal.Date{}, false
	}
	y, _ := strconv.Atoi(s[:4])
	m, _ := strconv.Atoi(s[4:6])
	d, _ := strconv.Atoi(s[6:8])
	return validDate(y, m, d)
}
