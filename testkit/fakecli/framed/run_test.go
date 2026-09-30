package framed_test

import (
	"bytes"
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

	"github.com/adro-project/adro/testkit/fakecli/framed"
)

func TestFramedChild(t *testing.T) {
	if os.Getenv("ADRO_FIXTURE_CHILD") != "1" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code, err := framed.Run(ctx, os.Stdin, os.Stdout, framed.Options{Mode: os.Getenv("ADRO_FIXTURE_MODE"), SessionID: "original-session", Runtime: "framed"})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(code)
}

func child(t *testing.T, mode string) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestFramedChild$")
	cmd.Env = []string{"ADRO_FIXTURE_CHILD=1", "ADRO_FIXTURE_MODE=" + mode}
	cmd.Stdin = strings.NewReader("{\"type\":\"execute\",\"request_id\":\"request-1\"}\n")
	cmd.WaitDelay = time.Second
	return cmd
}

func TestFramedRealProcessScenarios(t *testing.T) {
	for _, tc := range []struct {
		mode    string
		version float64
		session string
		reject  bool
		exit    int
	}{
		{"", 1, "original-session", false, 0},
		{"version", 2, "original-session", false, 0},
		{"session-mismatch", 1, "different-session", false, 0},
		{"resume-rejected", 1, "original-session", true, 0},
		{"exit-after-result", 1, "original-session", false, 1},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			cmd := child(t, tc.mode)
			output, err := cmd.Output()
			if tc.exit == 0 && err != nil {
				t.Fatal(err)
			}
			if tc.exit != 0 {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || exitErr.ExitCode() != tc.exit {
					t.Fatalf("child status: %v", err)
				}
			}
			decoder := json.NewDecoder(bytes.NewReader(output))
			var frames []map[string]any
			for decoder.More() {
				var frame map[string]any
				if err := decoder.Decode(&frame); err != nil {
					t.Fatal(err)
				}
				frames = append(frames, frame)
			}
			if len(frames) != 6 {
				t.Fatalf("frame count=%d", len(frames))
			}
			wantTypes := []string{"ready", "session", "tool_call", "tool_result", "usage", "result"}
			for i, frame := range frames {
				if frame["type"] != wantTypes[i] || frame["v"] != tc.version || (i > 0 && frame["request_id"] != "request-1") {
					t.Fatalf("frame %d: %v", i, frame)
				}
			}
			if frames[1]["session_id"] != tc.session || frames[5]["resume_rejected"] != tc.reject || frames[2]["call_id"] != frames[3]["call_id"] || frames[4]["input_tokens"] != float64(13) {
				t.Fatalf("protocol correlation or scenario evidence lost: %v", frames)
			}
		})
	}
}

func TestFramedTerminalCanPrecedeProcessExit(t *testing.T) {
	cmd := child(t, "linger")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	decoder := json.NewDecoder(io.LimitReader(stdout, 1<<20))
	for i := 0; i < 6; i++ {
		var frame map[string]any
		if err := decoder.Decode(&frame); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatal(err)
		}
		if i == 5 && frame["type"] != "result" {
			t.Fatal("fixture did not emit terminal evidence")
		}
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("fixture unexpectedly exited before parent cleanup: %v", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("killed lingering child reported clean exit")
	}
}

func TestFramedRejectsMalformedOrOversizedInput(t *testing.T) {
	for _, input := range []string{"", "invalid\n", "{}\n", "{\"type\":\"cancel\",\"request_id\":\"r\"}\n", strings.Repeat("x", 1<<20+1) + "\n"} {
		if _, err := framed.Run(context.Background(), strings.NewReader(input), io.Discard, framed.Options{SessionID: "s", Runtime: "framed"}); err == nil {
			t.Errorf("malformed request accepted (%d bytes)", len(input))
		}
	}
}

func TestFramedCancellationValidatesCorrelation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, id := range []string{"r", "other"} {
		input := fmt.Sprintf("{\"type\":\"execute\",\"request_id\":\"r\"}\n{\"type\":\"cancel\",\"request_id\":%q}\n", id)
		_, err := framed.Run(ctx, strings.NewReader(input), io.Discard, framed.Options{Mode: "cancel", SessionID: "s", Runtime: "framed"})
		if (id == "r") != errors.Is(err, context.Canceled) || err == nil {
			t.Fatalf("cancel correlation %s: %v", id, err)
		}
	}
}
