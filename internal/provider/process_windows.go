//go:build windows

package provider

import "os/exec"

func configureLocalCommand(_ *exec.Cmd) {}

func cancelLocalCommand(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func terminateLocalCommand(cmd *exec.Cmd) error {
	return cancelLocalCommand(cmd)
}
