package sjson

import (
	"sync"
	"testing"

	"github.com/tidwall/gjson"
)

// sameBackingArray reports whether a and b share the same underlying array.
func sameBackingArray(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == 0 && len(b) == 0
	}
	return &a[0] == &b[0]
}

// TestSetTopLevelArrayIndex pins the replace/append semantics for numeric
// paths against a top-level array.
func TestSetTopLevelArrayIndex(t *testing.T) {
	t.Run("in-bounds index replaces in place semantically", func(t *testing.T) {
		got, err := SetBytes([]byte(`[1,2,3]`), "1", 9)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != `[1,9,3]` {
			t.Fatalf("expected '[1,9,3]', got '%s'", got)
		}
	})
	t.Run("out-of-bounds index pads with null", func(t *testing.T) {
		// Replacing index 5 of a 2-element array does not fail and does
		// not ignore the write: the array is extended with null padding.
		got, err := SetBytes([]byte(`[1,2]`), "5", 9)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != `[1,2,null,null,null,9]` {
			t.Fatalf("expected '[1,2,null,null,null,9]', got '%s'", got)
		}
	})
	t.Run("out-of-bounds index with Optimistic behaves the same", func(t *testing.T) {
		got, err := SetBytesOptions([]byte(`[1,2]`), "5", 9,
			&Options{Optimistic: true})
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != `[1,2,null,null,null,9]` {
			t.Fatalf("expected '[1,2,null,null,null,9]', got '%s'", got)
		}
	})
	t.Run("adjacent index appends without padding", func(t *testing.T) {
		got, err := SetBytes([]byte(`[1,2]`), "2", 9)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != `[1,2,9]` {
			t.Fatalf("expected '[1,2,9]', got '%s'", got)
		}
	})
	t.Run("minus one appends", func(t *testing.T) {
		got, err := SetBytes([]byte(`[1,2]`), "-1", 9)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != `[1,2,9]` {
			t.Fatalf("expected '[1,2,9]', got '%s'", got)
		}
	})
	t.Run("delete out-of-bounds index is a no-op", func(t *testing.T) {
		in := []byte(`[1,2]`)
		got, err := DeleteBytes(in, "5")
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != `[1,2]` {
			t.Fatalf("expected '[1,2]', got '%s'", got)
		}
	})
}

// TestSetBytesSetLiteralParity pins that Set and SetBytes produce identical
// JSON for number, bool and null literals, and pins the exact encoding of
// each literal so the parity check cannot silently drift together.
func TestSetBytesSetLiteralParity(t *testing.T) {
	cases := []struct {
		name  string
		value interface{}
		want  string
	}{
		{"int", 42, `{"k":42}`},
		{"negative int64", int64(-7), `{"k":-7}`},
		{"float64", float64(0.1), `{"k":0.1}`},
		// float32 is widened through float64 before formatting, so the
		// stored value carries the float32 rounding error. This is the
		// current behavior; do not "fix" the expectation silently.
		{"float32 widened", float32(0.1), `{"k":0.10000000149011612}`},
		{"true", true, `{"k":true}`},
		{"false", false, `{"k":false}`},
		{"nil becomes null", nil, `{"k":null}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fromBytes, err := SetBytes([]byte(`{"k":0}`), "k", c.value)
			if err != nil {
				t.Fatal(err)
			}
			fromString, err := Set(`{"k":0}`, "k", c.value)
			if err != nil {
				t.Fatal(err)
			}
			if string(fromBytes) != c.want {
				t.Fatalf("SetBytes: expected '%s', got '%s'", c.want, fromBytes)
			}
			if fromString != c.want {
				t.Fatalf("Set: expected '%s', got '%s'", c.want, fromString)
			}
		})
	}
}

// TestReplaceFirstVsAll pins how many matches a replace touches: a plain
// path or a single-match query replaces only the first match, while the
// `#(...)#` query form replaces every match.
func TestReplaceFirstVsAll(t *testing.T) {
	json := `[{"k":"a","v":1},{"k":"a","v":2}]`
	t.Run("simple query replaces only the first match", func(t *testing.T) {
		got, err := Set(json, `#(k="a").v`, 9)
		if err != nil {
			t.Fatal(err)
		}
		if got != `[{"k":"a","v":9},{"k":"a","v":2}]` {
			t.Fatalf("expected only first match replaced, got '%s'", got)
		}
	})
	t.Run("hash-suffix query replaces all matches", func(t *testing.T) {
		got, err := Set(json, `#(k="a")#.v`, 9)
		if err != nil {
			t.Fatal(err)
		}
		if got != `[{"k":"a","v":9},{"k":"a","v":9}]` {
			t.Fatalf("expected all matches replaced, got '%s'", got)
		}
	})
	t.Run("plain path replaces exactly one occurrence", func(t *testing.T) {
		got, err := Set(`{"a":{"b":1},"c":{"b":1}}`, "a.b", 2)
		if err != nil {
			t.Fatal(err)
		}
		if got != `{"a":{"b":2},"c":{"b":1}}` {
			t.Fatalf("expected single replacement, got '%s'", got)
		}
	})
}

// TestReplaceInPlaceEscapedKeys pins the interaction between escaped keys
// and Options{Optimistic: true, ReplaceInPlace: true}.
func TestReplaceInPlaceEscapedKeys(t *testing.T) {
	t.Run("in-place replace mutates the input buffer", func(t *testing.T) {
		in := []byte(`{"a":"hello"}`)
		out, err := SetBytesOptions(in, "a", "hi",
			&Options{Optimistic: true, ReplaceInPlace: true})
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != `{"a":"hi"}` {
			t.Fatalf("expected '{\"a\":\"hi\"}', got '%s'", out)
		}
		if !sameBackingArray(out, in) {
			t.Fatal("expected output to alias the input buffer")
		}
		// The tail of the input beyond the result length is clobbered
		// garbage; the input must be considered invalid afterwards.
		if string(in) == `{"a":"hello"}` {
			t.Fatal("expected input buffer to be modified in place")
		}
	})
	t.Run("in-place replace falls back to allocation when value grows", func(t *testing.T) {
		in := []byte(`{"a":"hi"}`)
		out, err := SetBytesOptions(in, "a", "hello",
			&Options{Optimistic: true, ReplaceInPlace: true})
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != `{"a":"hello"}` {
			t.Fatalf("expected '{\"a\":\"hello\"}', got '%s'", out)
		}
		if sameBackingArray(out, in) {
			t.Fatal("expected a new backing array when the value grows")
		}
		if string(in) != `{"a":"hi"}` {
			t.Fatalf("expected input untouched, got '%s'", in)
		}
	})
	t.Run("escaped dot key is replaced in place", func(t *testing.T) {
		in := []byte(`{"app.token":"abc"}`)
		out, err := SetBytesOptions(in, `app\.token`, "xyz",
			&Options{Optimistic: true, ReplaceInPlace: true})
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != `{"app.token":"xyz"}` {
			t.Fatalf("expected '{\"app.token\":\"xyz\"}', got '%s'", out)
		}
		if !sameBackingArray(out, in) {
			t.Fatal("expected escaped-dot key to use the in-place path")
		}
	})
	t.Run("escaped colon key silently skips the in-place path", func(t *testing.T) {
		// NOTE: asymmetry with the escaped-dot case above. ':' makes the
		// path fail the optimistic-path character check, so even with
		// ReplaceInPlace set the write allocates and the input is left
		// intact. Pinning current behavior; if this is considered a bug
		// it should be fixed in the implementation, not here.
		in := []byte(`{"a:b":"abc"}`)
		out, err := SetBytesOptions(in, `a\:b`, "xyz",
			&Options{Optimistic: true, ReplaceInPlace: true})
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != `{"a:b":"xyz"}` {
			t.Fatalf("expected '{\"a:b\":\"xyz\"}', got '%s'", out)
		}
		if sameBackingArray(out, in) {
			t.Fatal("expected escaped-colon key to allocate a new buffer")
		}
		if string(in) != `{"a:b":"abc"}` {
			t.Fatalf("expected input untouched, got '%s'", in)
		}
	})
}

// TestDeleteNonExistentPath pins that deleting a missing path returns the
// input's own backing array unmodified, making the result safe to share
// with the input.
func TestDeleteNonExistentPath(t *testing.T) {
	t.Run("missing key returns the same backing array", func(t *testing.T) {
		in := []byte(`{"a":1}`)
		out, err := DeleteBytes(in, "b")
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != `{"a":1}` {
			t.Fatalf("expected input unchanged, got '%s'", out)
		}
		if !sameBackingArray(out, in) {
			t.Fatal("expected output to alias the input backing array")
		}
	})
	t.Run("missing nested path returns the same backing array", func(t *testing.T) {
		in := []byte(`{"a":1}`)
		out, err := DeleteBytes(in, "a.b.c")
		if err != nil {
			t.Fatal(err)
		}
		if !sameBackingArray(out, in) {
			t.Fatal("expected output to alias the input backing array")
		}
	})
	t.Run("missing array element returns the same backing array", func(t *testing.T) {
		in := []byte(`[1,2]`)
		out, err := DeleteBytes(in, "5")
		if err != nil {
			t.Fatal(err)
		}
		if !sameBackingArray(out, in) {
			t.Fatal("expected output to alias the input backing array")
		}
	})
	t.Run("complex path delete errors and returns a copy", func(t *testing.T) {
		// Query/wildcard paths are rejected for delete. The returned
		// bytes equal the input but are a fresh copy, not an alias.
		in := []byte(`{"a":1}`)
		out, err := DeleteBytes(in, "a.#")
		if err == nil {
			t.Fatal("expected an error for a complex delete path")
		}
		if string(out) != `{"a":1}` {
			t.Fatalf("expected input content, got '%s'", out)
		}
		if sameBackingArray(out, in) {
			t.Fatal("expected a copy, not the input backing array")
		}
	})
}

// TestConcurrentGetOnSharedBytes pins that concurrent gjson reads of a
// shared buffer are not disturbed by Set calls, because non-inplace Set
// never mutates its input. Run with -race for the guarantee to be checked.
func TestConcurrentGetOnSharedBytes(t *testing.T) {
	shared := []byte(`{"a":{"b":1}}`)
	snapshot := string(shared)

	const readers = 8
	const iterations = 2000

	var wg sync.WaitGroup
	errCh := make(chan string, readers)
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if v := gjson.GetBytes(shared, "a.b").Int(); v != 1 {
					errCh <- "reader observed mutated shared buffer"
					return
				}
			}
		}()
	}
	// Writer: repeatedly derive new values from the shared buffer.
	// Without ReplaceInPlace the input is never written to.
	for i := 0; i < iterations; i++ {
		if _, err := SetBytes(shared, "a.b", i); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Fatal(msg)
	}
	if string(shared) != snapshot {
		t.Fatal("SetBytes mutated its input buffer")
	}
}

// TestReplaceInPlaceInvalidatesSharedReads documents why the sharing
// guarantee above stops at ReplaceInPlace: the write side mutates the very
// buffer readers may be holding, so a shared []byte must never be written
// with ReplaceInPlace while readers exist. (A data race cannot be asserted
// deterministically, so this pins the mutation itself.)
func TestReplaceInPlaceInvalidatesSharedReads(t *testing.T) {
	shared := []byte(`{"a":{"b":100}}`)
	out, err := SetBytesOptions(shared, "a.b", 9,
		&Options{Optimistic: true, ReplaceInPlace: true})
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(out, "a.b").Int() != 9 {
		t.Fatalf("expected replaced value 9, got '%s'", out)
	}
	if string(shared) == `{"a":{"b":100}}` {
		t.Fatal("expected ReplaceInPlace to mutate the shared buffer")
	}
}
