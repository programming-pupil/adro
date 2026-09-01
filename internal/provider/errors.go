package provider

import (
	"context"
	"errors"
	"net"
)

var ErrConflict = errors.New("provider state conflict")

type ErrorCode string

const (
	ErrorConfiguration   ErrorCode = "configuration"
	ErrorTimeout         ErrorCode = "timeout"
	ErrorUnauthorized    ErrorCode = "unauthorized"
	ErrorForbidden       ErrorCode = "forbidden"
	ErrorNotFound        ErrorCode = "not_found"
	ErrorInvalidResponse ErrorCode = "invalid_response"
	ErrorUpstream        ErrorCode = "upstream_error"
	ErrorCapability      ErrorCode = "capability_unavailable"
)

// UpstreamError intentionally excludes URLs, response bodies, request
// headers, and native identifiers. It is safe to pass through API problems.
type UpstreamError struct {
	Code   ErrorCode
	Status int
}

// CapabilityError is returned when a gateway is reachable but explicitly does
// not advertise an operation. Treating this differently from a missing run
// keeps the API from inventing empty snapshots or retrying unsupported calls.
type CapabilityError struct {
	Capability     string
	AdapterVersion string
}

func (e *CapabilityError) Error() string {
	if e == nil {
		return string(ErrorCapability)
	}
	return "provider capability unavailable: " + e.Capability
}

func (e *UpstreamError) Error() string {
	if e == nil {
		return string(ErrorUpstream)
	}
	return "provider error: " + string(e.Code)
}

func (e *UpstreamError) Unwrap() error { return nil }

func ErrorCodeOf(err error) ErrorCode {
	if err == nil {
		return ""
	}
	var upstream *UpstreamError
	if errors.As(err, &upstream) && upstream != nil && upstream.Code != "" {
		return upstream.Code
	}
	var capability *CapabilityError
	if errors.As(err, &capability) {
		return ErrorCapability
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrorTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ErrorTimeout
	}
	return ErrorUpstream
}

func transportError(err error) *UpstreamError {
	code := ErrorUpstream
	if errors.Is(err, context.DeadlineExceeded) {
		code = ErrorTimeout
	} else {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			code = ErrorTimeout
		}
	}
	return &UpstreamError{Code: code}
}

func publicError(err error) string { return string(ErrorCodeOf(err)) }

func errorForStatus(status int) *UpstreamError {
	code := ErrorUpstream
	switch status {
	case 401:
		code = ErrorUnauthorized
	case 403:
		code = ErrorForbidden
	case 404:
		code = ErrorNotFound
	}
	return &UpstreamError{Code: code, Status: status}
}
