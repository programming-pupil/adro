package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func exampleList(t *testing.T) *List {
	t.Helper()
	salt := bytes.Repeat([]byte{42}, 16)
	digest := func(name string) string {
		sum := sha256.Sum256(append(append([]byte(nil), salt...), name...))
		return hex.EncodeToString(sum[:])
	}
	data := fmt.Sprintf("%x\ntok %s\nsub 10 %s\n", salt, digest("qxz"), digest("quartzunit"))
	d, err := Load(strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestTokenAndSubstringVariants(t *testing.T) {
	d := exampleList(t)
	for _, input := range []string{"qxz", "QXZ", "QxzWorker", "WorkerQXZ", "qxz2", "QUARTZUNIT", "QuartzUnit", "quartz-unit", "QUARTZ_UNIT", "quartz unit", "BeforeQuartzUnitAfter", "quartzunitworker", "quartz-units"} {
		if rule, _ := d.Hit(input); rule == "" {
			t.Errorf("missed variant %q", input)
		}
	}
	for _, input := range []string{"", "aqxzb", "require", "source", "ordinary42", "安全边界", "quartz(unit)", "Quartz.Unit"} {
		if rule, _ := d.Hit(input); rule != "" {
			t.Errorf("false positive %q: %s", input, rule)
		}
	}
}

func TestRulesRoundTripAndContainNoPlaintext(t *testing.T) {
	d := exampleList(t)
	for _, kind := range []string{"tok", "sub"} {
		row, err := d.Rule(kind, "QuartzUnit")
		if err != nil || strings.Contains(strings.ToLower(row), "quartz") {
			t.Fatalf("rule generation: %q, %v", row, err)
		}
		loaded, err := Load(strings.NewReader(fmt.Sprintf("%x\n%s\n", d.salt, row)))
		if err != nil {
			t.Fatal(err)
		}
		if rule, term := loaded.Hit("QuartzUnit"); rule == "" || term != "quartzunit" {
			t.Fatalf("round trip: rule=%s, term=%s", rule, term)
		}
	}
	for _, tc := range [][2]string{{"sub", "qxz"}, {"tok", ""}, {"tok", "a-b"}, {"tok", strings.Repeat("x", 257)}, {"bad", "valid"}} {
		if _, err := d.Rule(tc[0], tc[1]); err == nil {
			t.Errorf("invalid rule accepted: %v", tc)
		}
	}
}

func TestMalformedPolicyFailsClosed(t *testing.T) {
	salt := strings.Repeat("ab", 16) + "\n"
	hash := strings.Repeat("aa", 32)
	for _, data := range []string{"", "bad salt\n", salt, salt + "tok bad\n", salt + "sub 4 " + hash, salt + "sub 257 " + hash, salt + "tok " + hash + " extra", salt + "unknown " + hash, salt + "tok " + strings.ToUpper(hash), salt + "tok " + hash + "\ntok " + hash, salt + strings.Repeat("x", 70000)} {
		if _, err := Load(strings.NewReader(data)); err == nil {
			t.Errorf("invalid policy accepted (%d bytes)", len(data))
		}
	}
}

func TestExclusionIsNarrow(t *testing.T) {
	for _, path := range []string{"drivers/installed.yaml", "drivers/nested/installed.yaml"} {
		if !Excluded(path) {
			t.Errorf("deployment driver not excluded: %s", path)
		}
	}
	for _, path := range []string{"drivers/fakecli.yaml", "go.sum", "THIRD_PARTY_LICENSES/license.txt", "testdata/drivers/example", "drivers.go", "drivers-other/file"} {
		if Excluded(path) {
			t.Errorf("unexpected exemption: %s", path)
		}
	}
}

func TestConcurrentScanIsDeterministic(t *testing.T) {
	d := exampleList(t)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if rule, term := d.Hit("QuartzUnit"); rule == "" || term != "quartzunit" {
					t.Errorf("unexpected result %q/%q", rule, term)
				}
			}
		}()
	}
	wg.Wait()
}
