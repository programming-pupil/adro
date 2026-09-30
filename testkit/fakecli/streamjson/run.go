// Package streamjson provides recorded line-stream scenarios for driver tests.
// The fixture emits evidence; the driver under test must decide the outcome.
package streamjson

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

type Options struct {
	Mode      string
	SessionID string
}

// Run retains the result/completion pair and session proof from the frozen
// baseline. It does not implement the runtime's terminal or resume policy.
func Run(ctx context.Context, out io.Writer, opts Options) error {
	switch opts.Mode {
	case "", "linger", "result-only", "completion-only", "no-terminal", "no-proof", "wrong-proof":
	default:
		return fmt.Errorf("unknown stream fixture mode")
	}
	if opts.SessionID == "" {
		return fmt.Errorf("fixture session is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	if opts.Mode != "no-proof" {
		sessionID := opts.SessionID
		if opts.Mode == "wrong-proof" {
			sessionID += "-different"
		}
		if err := encoder.Encode(map[string]any{"type": "thread.started", "thread_id": sessionID}); err != nil {
			return err
		}
	}
	if opts.Mode != "completion-only" && opts.Mode != "no-terminal" {
		if err := encoder.Encode(map[string]any{
			"type": "item.completed",
			"item": map[string]any{
				"type": "agent_message",
				"text": `ADRO_RESULT_JSON={"outcome":"pass","reason_code":"done","summary":"done","evidence_ids":["done"],"fields":{}}`,
			},
		}); err != nil {
			return err
		}
	}
	if opts.Mode != "result-only" && opts.Mode != "no-terminal" {
		if err := encoder.Encode(map[string]any{"type": "turn.completed"}); err != nil {
			return err
		}
	}
	if opts.Mode == "linger" {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}
