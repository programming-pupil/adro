package budget_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/adro-project/adro/core/budget"
)

func TestNormalizeUsage(t *testing.T) {
	vector, err := budget.NormalizeUsage(7, 11, 13, 2, 17)
	want := budget.ResourceVector{Tokens: 18, ToolCalls: 2, WallTimeNanos: int64(13 * time.Millisecond), OutputBytes: 17, ConcurrencySlots: 1}
	if err != nil || vector != want {
		t.Fatalf("got %+v, %v; want %+v", vector, err, want)
	}
	vector, err = budget.NormalizeUsage(-7, 11, -13, 0, 0)
	if err != nil || vector.Tokens != 11 || vector.WallTimeNanos != 0 || vector.ConcurrencySlots != 1 {
		t.Fatalf("missing measurements: %+v, %v", vector, err)
	}
}

func TestNormalizeUsageRejectsOverflow(t *testing.T) {
	for _, input := range [][3]int64{{math.MaxInt64, 1, 0}, {0, 0, math.MaxInt64/int64(time.Millisecond) + 1}} {
		vector, err := budget.NormalizeUsage(input[0], input[1], input[2], 0, 0)
		if !errors.Is(err, budget.ErrResourceOverflow) || vector != (budget.ResourceVector{}) {
			t.Fatalf("overflow: %+v, %v", vector, err)
		}
	}
	vector, err := budget.NormalizeUsage(math.MaxInt64, 0, math.MaxInt64/int64(time.Millisecond), 0, 0)
	if err != nil || vector.Tokens != math.MaxInt64 {
		t.Fatalf("upper valid measurements: %+v, %v", vector, err)
	}
}
