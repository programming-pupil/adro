// Package identity defines the verified principal propagated across runtime
// boundaries. It contains no credential parsing; authentication adapters bind
// a validated Actor to a context only after credentials have been verified.
package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adro-project/adro/core/ids"
)

const MaxDelegationDepth = 16

var (
	ErrInvalidActor         = errors.New("invalid actor")
	ErrScopeEscalation      = errors.New("identity scope escalation")
	ErrDelegationTooDeep    = errors.New("identity delegation depth exceeded")
	ErrCredentialExpired    = errors.New("identity credential expired")
	ErrAudienceMismatch     = errors.New("identity audience mismatch")
	ErrTransitionUnapproved = errors.New("identity transition lacks required approval")
)

type ActorType string

const (
	ActorHuman     ActorType = "human"
	ActorService   ActorType = "service"
	ActorAgent     ActorType = "agent"
	ActorWorker    ActorType = "worker"
	ActorExtension ActorType = "extension"
)

func (t ActorType) Valid() bool {
	switch t {
	case ActorHuman, ActorService, ActorAgent, ActorWorker, ActorExtension:
		return true
	default:
		return false
	}
}

type TransitionMode string

const (
	TransitionDelegation    TransitionMode = "delegation"
	TransitionImpersonation TransitionMode = "impersonation"
	TransitionTakeover      TransitionMode = "takeover"
	TransitionBreakGlass    TransitionMode = "break_glass"
)

func (m TransitionMode) Valid() bool {
	switch m {
	case TransitionDelegation, TransitionImpersonation, TransitionTakeover, TransitionBreakGlass:
		return true
	default:
		return false
	}
}

type ActorRef struct {
	Type ActorType `json:"type"`
	ID   string    `json:"id"`
}

type Transition struct {
	From       ActorRef       `json:"from"`
	Mode       TransitionMode `json:"mode"`
	Reason     string         `json:"reason"`
	ApprovalID string         `json:"approval_id,omitempty"`
	At         time.Time      `json:"at"`
}

// Actor is the effective verified principal. Tenant and workspace scope are
// immutable across transitions. Delegation contains every prior effective
// principal so audits retain the original actor and the complete proxy chain.
type Actor struct {
	Type         ActorType    `json:"type"`
	ID           string       `json:"id"`
	TenantID     string       `json:"tenant_id"`
	WorkspaceID  string       `json:"workspace_id"`
	AuthnMethod  string       `json:"authn_method"`
	CredentialID string       `json:"credential_id"`
	Audience     string       `json:"audience"`
	IssuedAt     time.Time    `json:"issued_at"`
	ExpiresAt    time.Time    `json:"expires_at"`
	Delegation   []Transition `json:"delegation,omitempty"`
}

func (a Actor) Validate(now time.Time, audience string) error {
	if !a.Type.Valid() || !canonical(a.ID, 256) || !canonical(a.AuthnMethod, 128) ||
		!canonical(a.CredentialID, 256) || !canonical(a.Audience, 256) {
		return fmt.Errorf("%w: type, id, authentication method, credential, and audience are required", ErrInvalidActor)
	}
	if err := ids.Validate("tenant", a.TenantID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidActor, err)
	}
	if err := ids.Validate("workspace", a.WorkspaceID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidActor, err)
	}
	if a.IssuedAt.IsZero() || a.ExpiresAt.IsZero() || !a.ExpiresAt.After(a.IssuedAt) ||
		!a.IssuedAt.Equal(a.IssuedAt.UTC().Truncate(time.Microsecond)) ||
		!a.ExpiresAt.Equal(a.ExpiresAt.UTC().Truncate(time.Microsecond)) {
		return fmt.Errorf("%w: canonical issued and expiry times are required", ErrInvalidActor)
	}
	if now.IsZero() {
		return fmt.Errorf("%w: validation time is required", ErrInvalidActor)
	}
	now = now.UTC()
	if now.Before(a.IssuedAt) || !now.Before(a.ExpiresAt) {
		return ErrCredentialExpired
	}
	if strings.TrimSpace(audience) != "" && a.Audience != strings.TrimSpace(audience) {
		return ErrAudienceMismatch
	}
	if len(a.Delegation) > MaxDelegationDepth {
		return ErrDelegationTooDeep
	}
	var previousTransition time.Time
	for index, transition := range a.Delegation {
		if !transition.From.Type.Valid() || !canonical(transition.From.ID, 256) || !transition.Mode.Valid() ||
			!canonical(transition.Reason, 512) || transition.At.IsZero() ||
			!transition.At.Equal(transition.At.UTC().Truncate(time.Microsecond)) || transition.At.After(a.IssuedAt) || !transition.At.Before(a.ExpiresAt) ||
			(!previousTransition.IsZero() && transition.At.Before(previousTransition)) {
			return fmt.Errorf("%w: transition %d is incomplete", ErrInvalidActor, index)
		}
		if transition.Mode != TransitionDelegation && !canonical(transition.ApprovalID, 256) {
			return fmt.Errorf("%w: transition %d", ErrTransitionUnapproved, index)
		}
		previousTransition = transition.At
	}
	return nil
}

// TransitionTo creates a new effective principal while preserving immutable
// scope and the complete actor chain. Credential lifetime can only shrink.
func TransitionTo(parent Actor, target ActorRef, mode TransitionMode, reason, approvalID, authnMethod, credentialID, audience string, at, expiresAt time.Time) (Actor, error) {
	if err := parent.Validate(at, parent.Audience); err != nil {
		return Actor{}, err
	}
	if !target.Type.Valid() || !canonical(target.ID, 256) || !mode.Valid() || !canonical(strings.TrimSpace(reason), 512) {
		return Actor{}, fmt.Errorf("%w: target, mode, and reason are required", ErrInvalidActor)
	}
	if mode != TransitionDelegation && !canonical(strings.TrimSpace(approvalID), 256) {
		return Actor{}, ErrTransitionUnapproved
	}
	if len(parent.Delegation) >= MaxDelegationDepth {
		return Actor{}, ErrDelegationTooDeep
	}
	at = at.UTC().Truncate(time.Microsecond)
	expiresAt = expiresAt.UTC().Truncate(time.Microsecond)
	if expiresAt.After(parent.ExpiresAt) {
		return Actor{}, ErrScopeEscalation
	}
	delegation := append([]Transition(nil), parent.Delegation...)
	delegation = append(delegation, Transition{
		From: ActorRef{Type: parent.Type, ID: parent.ID}, Mode: mode,
		Reason: strings.TrimSpace(reason), ApprovalID: strings.TrimSpace(approvalID), At: at,
	})
	child := Actor{
		Type: target.Type, ID: strings.TrimSpace(target.ID), TenantID: parent.TenantID, WorkspaceID: parent.WorkspaceID,
		AuthnMethod: strings.TrimSpace(authnMethod), CredentialID: strings.TrimSpace(credentialID), Audience: strings.TrimSpace(audience),
		IssuedAt: at, ExpiresAt: expiresAt, Delegation: delegation,
	}
	if err := child.Validate(at, audience); err != nil {
		return Actor{}, err
	}
	return child, nil
}

func (a Actor) Original() ActorRef {
	if len(a.Delegation) == 0 {
		return ActorRef{Type: a.Type, ID: a.ID}
	}
	return a.Delegation[0].From
}

func (a Actor) Clone() Actor {
	a.Delegation = append([]Transition(nil), a.Delegation...)
	return a
}

type actorContextKey struct{}

// WithVerifiedActor binds an already-authenticated actor to a context. Only
// authentication boundaries should call this function; downstream code should
// use FromContext and must never reconstruct identity from request bodies.
func WithVerifiedActor(ctx context.Context, actor Actor, now time.Time, audience string) (context.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := actor.Validate(now, audience); err != nil {
		return nil, err
	}
	return context.WithValue(ctx, actorContextKey{}, actor.Clone()), nil
}

func FromContext(ctx context.Context) (Actor, bool) {
	if ctx == nil {
		return Actor{}, false
	}
	actor, ok := ctx.Value(actorContextKey{}).(Actor)
	if !ok {
		return Actor{}, false
	}
	return actor.Clone(), true
}

func canonical(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= maximum && !strings.ContainsAny(value, "\x00\r\n")
}
