# Reference bytes of the realtime contract

These `.bin` files are the contract's reference bytes: one encoded frame each,
named for what it holds. The Go test (`api/internal/realtime/wire/golden_test.go`)
and the TypeScript test (`ui/src/lib/realtime/wire/golden.test.ts`) both decode
every file and re-encode it to the same bytes, which is what keeps the two
implementations from drifting apart.

**Regenerate them only with an intentional contract change, in the same pull
request:**

    cd api && go run ./internal/realtime/wire/cmd_gen ../proto/testdata

`change_with_unknown_field.bin` is a `Change` carrying a field number this version
does not know; both decoders must skip it, not reject the frame.
