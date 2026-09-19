//go:build windows

package local

import (
	"errors"
	"os"
	"os/exec"
)

func configureCommand(cmd *exec.Cmd) {}

func cancelCommand(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

func supportsProcessTreeCancellation() bool { return false }
