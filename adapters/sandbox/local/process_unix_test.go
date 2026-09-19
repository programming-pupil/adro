//go:build aix || android || darwin || dragonfly || freebsd || hurd || illumos || ios || linux || netbsd || openbsd || solaris

package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/adro-project/adro/ports/sandbox"
)

func TestCancellationTerminatesDescendantProcessTree(t *testing.T) {
	broker := newTestSandbox(t)
	if !broker.cap.ProcessTreeCancellation {
		t.Skip("backend does not advertise process-tree cancellation")
	}
	pidPath := filepath.Join(broker.root, "child.pid")
	request := helperRequest(t, broker, "spawn-child", pidPath)
	if broker.cap.FilesystemEnforcement {
		request.FileGrants = []sandbox.FileGrant{{Path: pidPath, Write: true, Create: true}}
	}
	handle := prepareHelper(t, broker, request)
	stream, err := broker.Execute(context.Background(), handle)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var childPID int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(pidPath)
		if readErr == nil {
			childPID, readErr = strconv.Atoi(strings.TrimSpace(string(data)))
			if readErr == nil && childPID > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID <= 0 {
		_ = broker.Cancel(context.Background(), handle)
		_, _ = stream.Wait(context.Background())
		t.Fatal("helper did not publish descendant PID")
	}
	if err := broker.Cancel(context.Background(), handle); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if result, err := stream.Wait(context.Background()); !errors.Is(err, sandbox.ErrExecutionCancelled) || !result.Cancelled {
		t.Fatalf("cancel result=%+v err=%v", result, err)
	}

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(childPID, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := syscall.Kill(childPID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("descendant process %d survived process-tree cancellation: %v", childPID, err)
	}
}
