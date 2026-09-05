//go:build aix || android || darwin || dragonfly || freebsd || hurd || illumos || ios || linux || netbsd || openbsd || solaris

package provider

import (
	"os/exec"
	"syscall"
)

// configureLocalCommand isolates the provider in its own process group. Codex
// and other real executors may spawn helper processes; killing only the direct
// child leaves descendants holding stdout/stderr open and makes a deadline
// appear to hang until the graph watcher expires.
func configureLocalCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func cancelLocalCommand(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// terminateLocalCommand is used after a provider has emitted a committed
// terminal result. It intentionally uses the same process-group kill as the
// deadline path: a leaked MCP descendant must not keep the output pipe open.
func terminateLocalCommand(cmd *exec.Cmd) error {
	return cancelLocalCommand(cmd)
}
