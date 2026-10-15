// Command cmd_gen writes the contract's reference bytes, proto/testdata/*.bin.
//
// Run it only for an intentional contract change, in the same pull request as
// the change (see proto/testdata/README.md):
//
//	go run ./internal/realtime/wire/cmd_gen
//
// Every value is a literal, so the output never depends on the clock or on a
// random source.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"gopkg.aoctech.app/billing/api/internal/realtime/wire"
)

const (
	cursor    = "01JABCDEFGHJKMNPQRSTVWXYZ0"
	invoiceID = "in_01JABCDEFGHJKMNPQRSTVWXYZ1"
	occurred  = 1_760_000_000_000
	// unknownField is a field number no version of the contract uses: a future one.
	unknownField = 99
)

func server(b *wire.ServerFrame) proto.Message { return b }
func client(b *wire.ClientFrame) proto.Message { return b }

func main() {
	dir := "proto/testdata"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	fixtures := map[string]proto.Message{
		"auth_token": client(&wire.ClientFrame{Body: &wire.ClientFrame_Auth{Auth: &wire.Auth{
			Token: "eyJhbGciOiJSUzI1NiJ9.e30.sig", Space: "org:01a04ed6-1af9-745e-bcf7-d3b66fe52321",
			ResumeCursor: cursor, ClientProtocolVersion: 1,
		}}}),
		"auth_pay_link": client(&wire.ClientFrame{Body: &wire.ClientFrame_Auth{Auth: &wire.Auth{
			PayLink: "ctech.2.b64payload.b64mac", ClientProtocolVersion: 1,
		}}}),
		"reauth": client(&wire.ClientFrame{Body: &wire.ClientFrame_Reauth{Reauth: &wire.Reauth{Token: "eyJhbGciOiJSUzI1NiJ9.e30.sig2"}}}),
		"ping":   client(&wire.ClientFrame{Body: &wire.ClientFrame_Ping{Ping: &wire.Ping{ClientTimeMs: occurred}}}),
		"ready": server(&wire.ServerFrame{Body: &wire.ServerFrame_Ready{Ready: &wire.Ready{
			ProtocolVersion: 1, ServerTimeMs: occurred, ScopeKinds: []string{"tenant", "invoice"},
		}}}),
		"change_invoice_paid": server(&wire.ServerFrame{Body: &wire.ServerFrame_Change{Change: &wire.Change{
			Id: cursor, Type: "invoice.paid", Entity: "invoice", EntityId: invoiceID,
			Status: "PAID", Revision: 3, OccurredAtMs: occurred,
		}}}),
		"change_minimal": server(&wire.ServerFrame{Body: &wire.ServerFrame_Change{Change: &wire.Change{
			Id: cursor, Type: "customer.updated",
		}}}),
		"gap_cursor_too_old": server(&wire.ServerFrame{Body: &wire.ServerFrame_Gap{Gap: &wire.Gap{Reason: "cursor_too_old"}}}),
		"pong":               server(&wire.ServerFrame{Body: &wire.ServerFrame_Pong{Pong: &wire.Pong{ClientTimeMs: occurred, ServerTimeMs: occurred + 12}}}),
		"tick":               server(&wire.ServerFrame{Body: &wire.ServerFrame_Tick{Tick: &wire.Tick{Head: cursor}}}),
		"bye_draining":       server(&wire.ServerFrame{Body: &wire.ServerFrame_Bye{Bye: &wire.Bye{Reason: "draining", RetryAfterMs: 2000}}}),
		"hint":               server(&wire.ServerFrame{Body: &wire.ServerFrame_Hint{Hint: &wire.Hint{PollIntervalMs: 15000}}}),
	}

	encode := proto.MarshalOptions{Deterministic: true}
	for name, msg := range fixtures {
		raw, err := encode.Marshal(msg)
		if err != nil {
			fail(name, err)
		}
		write(dir, name, raw)
	}
	write(dir, "change_with_unknown_field", changeWithUnknownField(encode))
}

// changeWithUnknownField is a ServerFrame whose Change carries a field this
// version of the contract does not have, as a newer server would send it.
func changeWithUnknownField(encode proto.MarshalOptions) []byte {
	change, err := encode.Marshal(&wire.Change{Id: cursor, Type: "invoice.paid", Entity: "invoice", EntityId: invoiceID, Status: "PAID", Revision: 4})
	if err != nil {
		fail("change_with_unknown_field", err)
	}
	change = protowire.AppendTag(change, unknownField, protowire.BytesType)
	change = protowire.AppendString(change, "a field from the future")
	frame := protowire.AppendTag(nil, 2, protowire.BytesType) // ServerFrame.change
	return protowire.AppendBytes(frame, change)
}

func write(dir, name string, raw []byte) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(name, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".bin"), raw, 0o644); err != nil {
		fail(name, err)
	}
}

func fail(name string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
	os.Exit(1)
}
