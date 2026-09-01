//go:build !windows

package durable

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func withExclusive(path string, fn func() error) error {
	if fn == nil {
		return fmt.Errorf("durable lock callback is nil")
	}
	if path == "" {
		return fn()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open durable lock: %w", err)
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("acquire durable lock: %w", err)
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN) // best effort on close
	return fn()
}
