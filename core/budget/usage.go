package budget

import (
	"fmt"
	"math"
	"time"
)

// NormalizeUsage converts provider measurements to the resource ledger units.
// Negative token and duration measurements are treated as missing; counts and
// output sizes are supplied by the caller from the observed events and bytes.
func NormalizeUsage(inputTokens, outputTokens, durationMS, toolEvents, outputBytes int64) (ResourceVector, error) {
	tokens, err := safeUsageSum(inputTokens, outputTokens)
	if err != nil {
		return ResourceVector{}, err
	}
	if durationMS < 0 {
		durationMS = 0
	}
	if durationMS > math.MaxInt64/int64(time.Millisecond) {
		return ResourceVector{}, fmt.Errorf("%w: wall_time_nanos", ErrResourceOverflow)
	}
	return ResourceVector{Tokens: tokens, ToolCalls: toolEvents, WallTimeNanos: durationMS * int64(time.Millisecond), OutputBytes: outputBytes, ConcurrencySlots: 1}, nil
}

func safeUsageSum(values ...int64) (int64, error) {
	var total int64
	for _, value := range values {
		if value < 0 {
			continue
		}
		if value > math.MaxInt64-total {
			return 0, fmt.Errorf("%w: tokens", ErrResourceOverflow)
		}
		total += value
	}
	return total, nil
}
