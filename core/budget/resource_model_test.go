package budget_test

import (
	"errors"
	"math"
	"testing"

	"github.com/adro-project/adro/core/budget"
)

func TestResourceVectorRejectsNegativeAndOverflow(t *testing.T) {
	if err := (budget.ResourceVector{Tokens: -1}).Validate(); err == nil {
		t.Fatal("negative resource was accepted")
	}
	if _, err := (budget.ResourceVector{Tokens: math.MaxInt64}).Add(budget.ResourceVector{Tokens: 1}); !errors.Is(err, budget.ErrResourceOverflow) {
		t.Fatalf("overflow err=%v", err)
	}
	if excess := (budget.ResourceVector{Tokens: 11, ConcurrencySlots: 1}).Excess(budget.ResourceVector{Tokens: 10}); excess.Tokens != 1 || excess.ConcurrencySlots != 0 {
		t.Fatalf("unexpected excess=%+v", excess)
	}
}
