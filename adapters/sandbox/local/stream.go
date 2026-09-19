package local

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/adro-project/adro/ports/sandbox"
)

type executionStream struct {
	mu            sync.Mutex
	inputMu       sync.Mutex
	inputWriteMu  sync.Mutex
	events        chan sandbox.ExecutionEvent
	errors        chan error
	done          chan struct{}
	limitSignal   chan struct{}
	limitOnce     sync.Once
	finishOnce    sync.Once
	closeOnce     sync.Once
	limit         int64
	stdout        []byte
	stderr        []byte
	outputLimit   bool
	droppedEvents int64
	startedAt     time.Time
	finishedAt    time.Time
	result        sandbox.ExecutionResult
	err           error
	clock         func() time.Time
	cancel        context.CancelFunc
	input         io.WriteCloser
	inputClosed   bool
}

func newExecutionStream(limit int64, clock func() time.Time) *executionStream {
	if limit <= 0 {
		limit = defaultOutputLimit
	}
	return &executionStream{
		events: make(chan sandbox.ExecutionEvent, 32), errors: make(chan error, 1),
		done: make(chan struct{}), limitSignal: make(chan struct{}), limit: limit, clock: clock,
	}
}

func (s *executionStream) Events() <-chan sandbox.ExecutionEvent { return s.events }
func (s *executionStream) Errors() <-chan error                  { return s.errors }

func (s *executionStream) setCancel(cancel context.CancelFunc) {
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
}

// setInput must be called before the command is started. The stream owns the
// pipe for the rest of the process lifetime and closes it when execution
// finishes. Keeping ownership here prevents extension callers from retaining
// a raw process pipe and bypassing the stream lifecycle.
func (s *executionStream) setInput(input io.WriteCloser) {
	s.inputMu.Lock()
	if s.input == nil && !s.inputClosed {
		s.input = input
	} else if input != nil {
		_ = input.Close()
	}
	s.inputMu.Unlock()
}

func (s *executionStream) Send(ctx context.Context, data []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if len(data) == 0 {
		return nil
	}

	// A single writer at a time keeps JSON-RPC frames from interleaving. The
	// context watcher closes the OS pipe on cancellation, which unblocks a
	// blocked write when the child stops reading.
	s.inputWriteMu.Lock()
	defer s.inputWriteMu.Unlock()
	s.inputMu.Lock()
	input := s.input
	closed := s.inputClosed || input == nil
	s.inputMu.Unlock()
	if closed {
		return sandbox.ErrInputClosed
	}

	type writeResult struct {
		written int
		err     error
	}
	result := make(chan writeResult, 1)
	copyData := append([]byte(nil), data...)
	go func() {
		written, err := input.Write(copyData)
		if err == nil && written != len(copyData) {
			err = io.ErrShortWrite
		}
		result <- writeResult{written: written, err: err}
	}()
	select {
	case written := <-result:
		if written.err != nil {
			s.inputMu.Lock()
			closed := s.inputClosed
			s.inputMu.Unlock()
			if closed {
				return sandbox.ErrInputClosed
			}
		}
		return written.err
	case <-ctx.Done():
		_ = s.CloseInput()
		return ctx.Err()
	}
}

// CloseInput is idempotent. Once closure is requested, callers must not
// retry writes even when the underlying pipe reports a close error.
func (s *executionStream) CloseInput() error {
	s.inputMu.Lock()
	if s.inputClosed {
		s.inputMu.Unlock()
		return nil
	}
	s.inputClosed = true
	input := s.input
	s.input = nil
	s.inputMu.Unlock()
	if input == nil {
		return nil
	}
	if err := input.Close(); err != nil {
		return err
	}
	return nil
}

func (s *executionStream) markStarted(at time.Time) {
	s.mu.Lock()
	s.startedAt = at
	s.mu.Unlock()
}

func (s *executionStream) read(name string, reader io.Reader) {
	buffer := make([]byte, 32*1024)
	for {
		count, err := reader.Read(buffer)
		if count > 0 {
			chunk := append([]byte(nil), buffer[:count]...)
			s.mu.Lock()
			remaining := s.limit - int64(len(s.stdout)) - int64(len(s.stderr))
			if remaining <= 0 {
				s.outputLimit = true
				s.mu.Unlock()
				s.signalLimit()
				return
			}
			if int64(len(chunk)) > remaining {
				chunk = chunk[:remaining]
				s.outputLimit = true
			}
			if name == "stdout" {
				s.stdout = append(s.stdout, chunk...)
			} else {
				s.stderr = append(s.stderr, chunk...)
			}
			exceeded := s.outputLimit
			s.mu.Unlock()
			if len(chunk) > 0 {
				event := sandbox.ExecutionEvent{Stream: name, Data: append([]byte(nil), chunk...)}
				select {
				case s.events <- event:
				default:
					s.mu.Lock()
					s.droppedEvents++
					s.mu.Unlock()
				}
			}
			if exceeded {
				s.signalLimit()
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				select {
				case s.errors <- err:
				default:
				}
			}
			return
		}
	}
}

func (s *executionStream) signalLimit() {
	s.limitOnce.Do(func() { close(s.limitSignal) })
}

func (s *executionStream) limitExceeded() <-chan struct{} { return s.limitSignal }

func (s *executionStream) exceededLimit() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.outputLimit
}

func (s *executionStream) finish(result sandbox.ExecutionResult, err error) {
	s.finishOnce.Do(func() {
		_ = s.CloseInput()
		s.mu.Lock()
		result.Stdout = append([]byte(nil), s.stdout...)
		result.Stderr = append([]byte(nil), s.stderr...)
		result.StartedAt = s.startedAt
		result.FinishedAt = s.clock().UTC()
		result.OutputLimit = s.outputLimit
		result.DroppedEvents = s.droppedEvents
		s.result, s.err, s.finishedAt = result, err, result.FinishedAt
		s.mu.Unlock()
		close(s.done)
		close(s.events)
		close(s.errors)
	})
}

func (s *executionStream) Wait(ctx context.Context) (sandbox.ExecutionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		return cloneResult(s.result), s.err
	case <-ctx.Done():
		return sandbox.ExecutionResult{}, ctx.Err()
	}
}

func (s *executionStream) Close() error {
	s.closeOnce.Do(func() {
		_ = s.CloseInput()
		s.mu.Lock()
		cancel := s.cancel
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	})
	return nil
}

func cloneResult(result sandbox.ExecutionResult) sandbox.ExecutionResult {
	result.Stdout = append([]byte(nil), result.Stdout...)
	result.Stderr = append([]byte(nil), result.Stderr...)
	return result
}
