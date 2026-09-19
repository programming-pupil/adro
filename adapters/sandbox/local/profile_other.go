//go:build !darwin

package local

import "os/exec"

func wrapCommand(cmd *exec.Cmd, profile string, argv []string) *exec.Cmd { return cmd }
