package provider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecuteCodexAppServerRetriesCompletelyEmptyTurn(t *testing.T) {
	executable := writeFakeCodexAppServer(t, false)
	t.Setenv("ADRO_CODEX_EMPTY_TURN_RETRIES", "1")
	t.Setenv("ADRO_CODEX_EMPTY_TURN_BACKOFF", "1ms")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pid, output, err := executeCodexAppServer(ctx, executable, nil, "return evidence", t.TempDir(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	text := string(output)
	if pid < 1 || !strings.Contains(text, `"delay_ms":1`) || !strings.Contains(text, `"type":"codex.empty_turn.retry"`) || !strings.Contains(text, "ADRO_RESULT_JSON") {
		t.Fatalf("empty turn was not retried with retained evidence: pid=%d output=%s", pid, text)
	}
}

func TestExecuteCodexAppServerFailsAfterEmptyTurnRetryLimit(t *testing.T) {
	executable := writeFakeCodexAppServer(t, true)
	t.Setenv("ADRO_CODEX_EMPTY_TURN_RETRIES", "1")
	t.Setenv("ADRO_CODEX_EMPTY_TURN_BACKOFF", "1ms")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, output, err := executeCodexAppServer(ctx, executable, nil, "return evidence", t.TempDir(), "", false)
	if err == nil || !strings.Contains(err.Error(), "without assistant or tool output after 2 attempt(s)") {
		t.Fatalf("empty turns did not fail closed: err=%v output=%s", err, output)
	}
}

func TestCodexEmptyTurnRetryConfiguration(t *testing.T) {
	t.Setenv("ADRO_CODEX_EMPTY_TURN_RETRIES", "0")
	if got := codexEmptyTurnRetries(); got != 0 {
		t.Fatalf("retries=%d want=0", got)
	}
	t.Setenv("ADRO_CODEX_EMPTY_TURN_RETRIES", "invalid")
	if got := codexEmptyTurnRetries(); got != 2 {
		t.Fatalf("invalid retry configuration did not use default: %d", got)
	}
}

func TestCodexEmptyTurnRetryBackoff(t *testing.T) {
	t.Setenv("ADRO_CODEX_EMPTY_TURN_BACKOFF", "")
	for attempt, want := range []time.Duration{5 * time.Second, 15 * time.Second, 45 * time.Second, time.Minute, time.Minute} {
		if got := codexEmptyTurnRetryDelay(attempt + 1); got != want {
			t.Fatalf("attempt %d delay=%s want=%s", attempt+1, got, want)
		}
	}

	t.Setenv("ADRO_CODEX_EMPTY_TURN_BACKOFF", "2ms")
	if got := codexEmptyTurnRetryDelay(2); got != 6*time.Millisecond {
		t.Fatalf("configured delay=%s want=6ms", got)
	}
	t.Setenv("ADRO_CODEX_EMPTY_TURN_BACKOFF", "invalid")
	if got := codexEmptyTurnRetryDelay(1); got != 5*time.Second {
		t.Fatalf("invalid delay=%s want=5s", got)
	}
}

func writeFakeCodexAppServer(t *testing.T, alwaysEmpty bool) string {
	t.Helper()
	executable := filepath.Join(t.TempDir(), "codex")
	finalTurn := `
      printf '%s\n' '{"jsonrpc":"2.0","id":4,"result":{"turn":{"id":"turn-2"}}}'
      printf '%s\n' '{"jsonrpc":"2.0","method":"item/completed","params":{"threadId":"11111111-1111-4111-8111-111111111111","item":{"type":"agentMessage","text":"ADRO_RESULT_JSON={\"outcome\":\"pass\"}"}}}'
      printf '%s\n' '{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"11111111-1111-4111-8111-111111111111","turn":{"id":"turn-2","items":[],"status":"completed"}}}'
`
	if alwaysEmpty {
		finalTurn = `
      printf '%s\n' '{"jsonrpc":"2.0","id":4,"result":{"turn":{"id":"turn-2"}}}'
      printf '%s\n' '{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"11111111-1111-4111-8111-111111111111","turn":{"id":"turn-2","items":[],"status":"completed"}}}'
`
	}
	script := `#!/bin/sh
set -eu
turns=0
while IFS= read -r request; do
  case "$request" in
    *'"method":"initialize"'*)
      printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"serverInfo":{"name":"fake"}}}'
      ;;
    *'"method":"thread/start"'*)
      printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{"thread":{"id":"11111111-1111-4111-8111-111111111111"}}}'
      ;;
    *'"method":"turn/start"'*)
      turns=$((turns + 1))
      if [ "$turns" -eq 1 ]; then
        printf '%s\n' '{"jsonrpc":"2.0","id":3,"result":{"turn":{"id":"turn-1"}}}'
        printf '%s\n' '{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"11111111-1111-4111-8111-111111111111","turn":{"id":"turn-1","items":[],"status":"completed"}}}'
      else
` + finalTurn + `
      fi
      ;;
  esac
done
`
	if err := os.WriteFile(executable, []byte(script), 0o750); err != nil {
		t.Fatal(err)
	}
	return executable
}
