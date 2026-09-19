//go:build darwin

package local

import (
	"os/exec"
)

func wrapCommand(cmd *exec.Cmd, profile string, argv []string) *exec.Cmd {
	wrapped := exec.Command("sandbox-exec", "-p", profile, "--")
	wrapped.Args = append(wrapped.Args, argv...)
	wrapped.Dir = cmd.Dir
	wrapped.Env = cmd.Env
	return wrapped
}
