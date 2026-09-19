//go:build darwin

package local

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adro-project/adro/ports/sandbox"
)

func TestSeatbeltAllowsGrantedWriteAndDeniesUngrantWrite(t *testing.T) {
	broker := newTestSandbox(t)
	if broker.cap.Backend != "macos-seatbelt" {
		t.Skip("sandbox-exec is unavailable")
	}

	allowedPath := filepath.Join(broker.root, "allowed.txt")
	allowed := helperRequest(t, broker, "write", allowedPath, "allowed")
	allowed.RequiredLevel = sandbox.EnforcementNetwork
	allowed.FileGrants = []sandbox.FileGrant{{Path: allowedPath, Write: true, Create: true}}
	allowedStream, err := broker.Execute(context.Background(), prepareHelper(t, broker, allowed))
	if err != nil {
		t.Fatalf("execute allowed write: %v", err)
	}
	if result, err := allowedStream.Wait(context.Background()); err != nil {
		t.Fatalf("granted write failed: result=%+v err=%v", result, err)
	}
	data, err := os.ReadFile(allowedPath)
	if err != nil || string(data) != "allowed" {
		t.Fatalf("granted output data=%q err=%v", data, err)
	}

	outsidePath := filepath.Join(t.TempDir(), "denied.txt")
	denied := helperRequest(t, broker, "write", outsidePath, "denied")
	denied.RequiredLevel = sandbox.EnforcementNetwork
	deniedStream, err := broker.Execute(context.Background(), prepareHelper(t, broker, denied))
	if err != nil {
		t.Fatalf("execute denied write: %v", err)
	}
	if result, err := deniedStream.Wait(context.Background()); err == nil || result.ExitCode == 0 {
		t.Fatalf("ungranted write succeeded: result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(outsidePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ungranted path exists or stat failed unexpectedly: %v", err)
	}
}

// Threat ID: TM-SBX-002
func TestSeatbeltDeniesOutboundNetworkByDefault(t *testing.T) {
	broker := newTestSandbox(t)
	if broker.cap.Backend != "macos-seatbelt" {
		t.Skip("sandbox-exec is unavailable")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	request := helperRequest(t, broker, "dial-must-fail", listener.Addr().String())
	request.RequiredLevel = sandbox.EnforcementNetwork
	stream, err := broker.Execute(context.Background(), prepareHelper(t, broker, request))
	if err != nil {
		t.Fatalf("execute network probe: %v", err)
	}
	result, err := stream.Wait(context.Background())
	if err != nil || string(result.Stdout) != "denied" {
		t.Fatalf("network was not denied: result=%+v err=%v", result, err)
	}
}

func TestSeatbeltRejectsNetworkGrantWithoutControlledProxy(t *testing.T) {
	broker := newTestSandbox(t)
	if broker.cap.Backend != "macos-seatbelt" {
		t.Skip("sandbox-exec is unavailable")
	}
	request := helperRequest(t, broker, "output")
	request.RequiredLevel = sandbox.EnforcementNetwork
	request.NetworkGrants = []sandbox.NetworkGrant{{
		Domain: "api.example.test", Ports: []int{443}, Protocol: "https", Purpose: "model",
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	}}
	if _, err := broker.Prepare(context.Background(), request); !errors.Is(err, sandbox.ErrUnsupportedEnforcement) {
		t.Fatalf("unfaithful Seatbelt grant returned %v", err)
	}
}
