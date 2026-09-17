package encoding

import (
	"math"
	"testing"
)

func TestCanonicalizeEquivalentJSON(t *testing.T) {
	inputs := [][]byte{
		[]byte(`{"b":1.2300,"a":1e3,"text":"世界"}`),
		[]byte(`{ "text":"世界", "a":1000.0, "b":1.23 }`),
	}
	first, err := Canonicalize(inputs[0])
	if err != nil {
		t.Fatal(err)
	}
	second, err := Canonicalize(inputs[1])
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) || string(first) != `{"a":1000,"b":1.23,"text":"世界"}` {
		t.Fatalf("canonical values differ: %s != %s", first, second)
	}
}

func TestMarshalRejectsNonFiniteNumbers(t *testing.T) {
	if _, err := Marshal(map[string]any{"value": math.Inf(1)}); err == nil {
		t.Fatal("expected infinity to be rejected")
	}
}

func TestDigestGolden(t *testing.T) {
	digest, err := Digest(map[string]any{"a": 1, "b": []any{true, "x"}})
	if err != nil {
		t.Fatal(err)
	}
	const want = "63e8063d9dc6f0fd5a24b4706818a165fd57c3531b74466cf5dea62bff09b0b6"
	if digest != want {
		t.Fatalf("digest=%s want=%s", digest, want)
	}
}
