package ids

import (
	"errors"
	"testing"
)

func TestValidate(t *testing.T) {
	for _, value := range []string{"tenant-1", "session:01", "event_2026.09"} {
		if err := Validate("test", value); err != nil {
			t.Fatalf("Validate(%q): %v", value, err)
		}
	}
	for _, value := range []string{"", " leading", "space inside", "path/value"} {
		if err := Validate("test", value); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Validate(%q) error=%v", value, err)
		}
	}
}
