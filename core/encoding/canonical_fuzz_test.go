package encoding

import "testing"

func FuzzCanonicalizeIsIdempotent(f *testing.F) {
	for _, seed := range []string{
		`null`,
		`{"b":1.0,"a":[true,"世界"]}`,
		`[1e3,0.0100,-0]`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		first, err := Canonicalize([]byte(input))
		if err != nil {
			return
		}
		second, err := Canonicalize(first)
		if err != nil {
			t.Fatalf("canonical output was not valid: %v", err)
		}
		if string(first) != string(second) {
			t.Fatalf("canonicalization changed on second pass: %s != %s", first, second)
		}
	})
}
