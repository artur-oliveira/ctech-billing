package wire

import (
	"testing"

	"google.golang.org/protobuf/proto"
)

// FuzzClientFrame: whatever a client sends, decoding it never panics and the
// result re-encodes. Run as a plain test over the seeds in every CI build, and
// with -fuzz nightly (.github/workflows/realtime-fuzz.yml).
func FuzzClientFrame(f *testing.F) {
	for _, seed := range []*ClientFrame{
		{Body: &ClientFrame_Auth{Auth: &Auth{Token: "t", PayLink: "p", Space: "org:x", Mode: "live", ResumeCursor: "01A"}}},
		{Body: &ClientFrame_Reauth{Reauth: &Reauth{Token: "t"}}},
		{Body: &ClientFrame_Ping{Ping: &Ping{ClientTimeMs: 1}}},
		{},
	} {
		b, _ := proto.Marshal(seed)
		f.Add(b)
	}
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	f.Add([]byte{0x0a, 0xff, 0xff, 0xff, 0x7f})
	f.Fuzz(func(t *testing.T, raw []byte) {
		var frame ClientFrame
		if err := proto.Unmarshal(raw, &frame); err != nil {
			return
		}
		// What decoded must be readable through every accessor the gateway uses.
		_ = frame.GetAuth().GetToken()
		_ = frame.GetAuth().GetResumeCursor()
		_ = frame.GetReauth().GetToken()
		_ = frame.GetPing().GetClientTimeMs()
		if _, err := proto.Marshal(&frame); err != nil {
			t.Fatalf("a decoded frame does not re-encode: %v", err)
		}
	})
}
