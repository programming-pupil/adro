//go:build windows

package durable

import "fmt"

func withExclusive(_ string, fn func() error) error {
	if fn == nil {
		return fmt.Errorf("durable lock callback is nil")
	}
	return fn()
}
