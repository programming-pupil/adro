package errs_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/adro-project/adro/core/errs"
)

func TestKindOf(t *testing.T) {
	for k := errs.KindInternal; k <= errs.KindUnsupported; k++ {
		err := fmt.Errorf("outer: %w", &errs.Error{Kind: k, Op: "journal.append", Code: "append.rejected"})
		if got := errs.KindOf(err); got != k {
			t.Fatalf("kind %d through wrapper: got %d", k, got)
		}
	}
	var nilClassified *errs.Error
	for _, err := range []error{nil, errors.New("unavailable"), nilClassified, &errs.Error{Kind: 255}} {
		if got := errs.KindOf(err); got != errs.KindInternal {
			t.Fatalf("unclassified error %v: got %d", err, got)
		}
	}
	inner := &errs.Error{Kind: errs.KindUnavailable}
	outer := &errs.Error{Kind: errs.KindDenied, Err: inner}
	if got := errs.KindOf(outer); got != errs.KindDenied {
		t.Fatalf("outer denial lost: %d", got)
	}
	if got := errs.KindOf(errors.Join(errors.New("context"), inner)); got != errs.KindUnavailable {
		t.Fatalf("joined classification lost: %d", got)
	}
}

func TestRetryable(t *testing.T) {
	for k := errs.KindInternal; k <= errs.KindUnsupported; k++ {
		err := fmt.Errorf("wrapped: %w", &errs.Error{Kind: k})
		want := k == errs.KindUnavailable || k == errs.KindAmbiguous
		if got := errs.Retryable(err); got != want {
			t.Fatalf("kind %d: retryable=%v, want %v", k, got, want)
		}
	}
	if errs.Retryable(nil) || errs.Retryable(errors.New("retryable")) {
		t.Fatal("unclassified errors must not request retries")
	}
}

func TestErrorKeepsCauseWithoutExposingIt(t *testing.T) {
	cause := errors.New("private-connection-material")
	err := &errs.Error{Kind: errs.KindUnavailable, Op: "journal.append", Code: "store.unavailable", Detail: map[string]string{"secret": "private-detail"}, Err: cause}
	if got := err.Error(); got != "journal.append: store.unavailable" {
		t.Fatalf("diagnostic includes unexpected data: %q", got)
	}
	if !errors.Is(err, cause) {
		t.Fatal("cause was lost")
	}
	var target *errs.Error
	if !errors.As(fmt.Errorf("wrapped: %w", err), &target) || target != err {
		t.Fatal("typed inspection was lost")
	}
	var absent *errs.Error
	if absent.Unwrap() != nil || absent.Error() != "internal: nil_error" {
		t.Fatal("typed nil error is unsafe")
	}
}
