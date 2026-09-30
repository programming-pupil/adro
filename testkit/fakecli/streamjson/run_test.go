package streamjson_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/adro-project/adro/testkit/fakecli/streamjson"
)

func TestStreamChild(t *testing.T) {
	if os.Getenv("ADRO_STREAM_CHILD") != "1" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := streamjson.Run(ctx, os.Stdout, streamjson.Options{Mode: os.Getenv("ADRO_STREAM_MODE"), SessionID: "session-original"})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func child(t *testing.T, mode string) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestStreamChild$")
	cmd.Env = []string{"ADRO_STREAM_CHILD=1", "ADRO_STREAM_MODE=" + mode}
	cmd.WaitDelay = time.Second
	return cmd
}

// These assertions check the fixture's evidence, not the future driver's policy.
func TestStreamTerminalAndProofInputs(t *testing.T) {
	for _, tc := range []struct {
		mode       string
		session    string
		result     bool
		completion bool
	}{
		{"", "session-original", true, true},
		{"result-only", "session-original", true, false},
		{"completion-only", "session-original", false, true},
		{"no-terminal", "session-original", false, false},
		{"no-proof", "", true, true},
		{"wrong-proof", "session-original-different", true, true},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			output, err := child(t, tc.mode).Output()
			if err != nil {
				t.Fatal(err)
			}
			var session string
			var result, completion bool
			scanner := bufio.NewScanner(strings.NewReader(string(output)))
			for scanner.Scan() {
				var event struct {
					Type     string `json:"type"`
					ThreadID string `json:"thread_id"`
					Item     struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"item"`
				}
				if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
					t.Fatal(err)
				}
				switch event.Type {
				case "thread.started":
					session = event.ThreadID
				case "item.completed":
					const prefix = "ADRO_RESULT_JSON="
					payload := strings.TrimPrefix(event.Item.Text, prefix)
					if event.Item.Type != "agent_message" || payload == event.Item.Text || !json.Valid([]byte(payload)) {
						t.Fatal("result envelope input was lost")
					}
					result = true
				case "turn.completed":
					completion = true
				default:
					t.Fatalf("unexpected event %q", event.Type)
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			if session != tc.session || result != tc.result || completion != tc.completion {
				t.Fatalf("evidence: session=%q result=%v completion=%v", session, result, completion)
			}
		})
	}
}

func TestStreamTerminalLeavesLiveChild(t *testing.T) {
	cmd := child(t, "linger")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reaped := false
	t.Cleanup(func() {
		if !reaped {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	scanner := bufio.NewScanner(stdout)
	for i := 0; i < 3; i++ {
		if !scanner.Scan() {
			t.Fatalf("missing evidence before cleanup: %v", scanner.Err())
		}
	}
	if !strings.Contains(scanner.Text(), `"turn.completed"`) {
		t.Fatal("last event did not contain completion")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("child did not stay alive after completion: %v", err)
	}
	err = cmd.Wait()
	reaped = true
	if err == nil {
		t.Fatal("killed child reported a clean exit")
	}
}

func TestStreamCancellationAndWriterFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := streamjson.Run(ctx, io.Discard, streamjson.Options{SessionID: "s"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled run: %v", err)
	}
	writer, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := streamjson.Run(context.Background(), writer, streamjson.Options{SessionID: "s"}); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed writer error was lost: %v", err)
	}
	for _, opts := range []streamjson.Options{{}, {Mode: "unknown", SessionID: "s"}} {
		if err := streamjson.Run(context.Background(), io.Discard, opts); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
}
