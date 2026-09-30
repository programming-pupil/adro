package budget

import (
	"fmt"
	"time"
)

type Budget struct {
	Tokens     int64         `json:"tokens,omitempty"`
	ToolCalls  int           `json:"tool_calls,omitempty"`
	CostCents  int64         `json:"cost_cents,omitempty"`
	Duration   time.Duration `json:"duration,omitempty"`
	Concurrent int           `json:"concurrent,omitempty"`
}

func Validate(b Budget, label string) error {
	if b.Tokens < 0 || b.ToolCalls < 0 || b.CostCents < 0 || b.Duration < 0 || b.Concurrent < 0 {
		return fmt.Errorf("%s cannot contain negative values", label)
	}
	return nil
}
