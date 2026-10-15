package wire

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

const goldenDir = "../../../../proto/testdata"

// fixtureCount is the whole set: a fixture that goes missing must fail the test,
// not shrink it.
const fixtureCount = 13

func isClientFrame(name string) bool {
	return strings.HasPrefix(name, "auth") || strings.HasPrefix(name, "reauth") || name == "ping.bin"
}

func TestEveryGoldenFileDecodesAndReEncodesToTheSameBytes(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(goldenDir, "*.bin"))
	if err != nil || len(files) != fixtureCount {
		t.Fatalf("expected the whole fixture set (%d), found %d (%v)", fixtureCount, len(files), err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(f)
		var msg proto.Message = &ServerFrame{}
		if isClientFrame(name) {
			msg = &ClientFrame{}
		}
		if err := proto.Unmarshal(raw, msg); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		again, err := proto.MarshalOptions{Deterministic: true}.Marshal(msg)
		if err != nil || !bytes.Equal(raw, again) {
			t.Errorf("%s re-encodes differently: %x vs %x (%v)", name, raw, again, err)
		}
	}
}

func TestAnUnknownFieldIsSkippedNotRejected(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(goldenDir, "change_with_unknown_field.bin"))
	if err != nil {
		t.Fatal(err)
	}
	var f ServerFrame
	if err := proto.Unmarshal(raw, &f); err != nil || f.GetChange().GetId() == "" || f.GetChange().GetType() == "" {
		t.Fatalf("%v %+v", err, f.GetChange())
	}
}

// What the fixtures hold, spelled out: a reader who changes the generator sees
// here what each file is meant to be.
func TestGoldenFilesHoldWhatTheirNamesSay(t *testing.T) {
	read := func(name string, msg proto.Message) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(goldenDir, name+".bin"))
		if err != nil {
			t.Fatal(err)
		}
		if err := proto.Unmarshal(raw, msg); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	var c ClientFrame
	read("auth_pay_link", &c)
	if c.GetAuth().GetPayLink() == "" || c.GetAuth().GetToken() != "" {
		t.Errorf("auth_pay_link = %+v", c.GetAuth())
	}
	read("auth_token", &c)
	if c.GetAuth().GetToken() == "" || c.GetAuth().GetPayLink() != "" {
		t.Errorf("auth_token = %+v", c.GetAuth())
	}
	var s ServerFrame
	read("change_invoice_paid", &s)
	if ch := s.GetChange(); ch.GetType() != "invoice.paid" || ch.GetEntity() != "invoice" || ch.GetStatus() != "PAID" || ch.GetRevision() == 0 {
		t.Errorf("change_invoice_paid = %+v", ch)
	}
	read("change_minimal", &s)
	if ch := s.GetChange(); ch.GetId() == "" || ch.GetType() == "" || ch.GetEntityId() != "" || ch.GetRevision() != 0 {
		t.Errorf("change_minimal = %+v", ch)
	}
	read("gap_cursor_too_old", &s)
	if s.GetGap().GetReason() != "cursor_too_old" {
		t.Errorf("gap = %+v", s.GetGap())
	}
	read("bye_draining", &s)
	if s.GetBye().GetReason() != "draining" || s.GetBye().GetRetryAfterMs() == 0 {
		t.Errorf("bye = %+v", s.GetBye())
	}
}
