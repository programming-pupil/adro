package mentions

import (
	"context"
	"fmt"
	"testing"
)

func TestStructuredMentionParsingDedupAndAll(t *testing.T) {
	p, err := Parse(`请看 [@研发](mention://agent/550e8400-e29b-41d4-a716-446655440000) [@研发](mention://agent/550e8400-e29b-41d4-a716-446655440000) [@all](mention://all/all)`)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Targets()) != 2 {
		t.Fatalf("targets=%d", len(p.Targets()))
	}
	if p.SourceHash == "" {
		t.Fatal("missing source hash")
	}
}

func TestRenderOnlyMentionsNeverBecomeInvocationTargets(t *testing.T) {
	parsed, err := Parse("[@member](mention://member/550e8400-e29b-41d4-a716-446655440000) [@issue](mention://issue/6ba7b810-9dad-41d1-80b4-00c04fd430c8)")
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Targets()) != 2 || len(parsed.InvocationTargets()) != 0 {
		t.Fatalf("render-only targets=%+v invocation=%+v", parsed.Targets(), parsed.InvocationTargets())
	}
	plan, err := ComputeTriggers(context.Background(), TriggerInput{WorkspaceID: "w", CommentID: "c", Content: "[@member](mention://member/550e8400-e29b-41d4-a716-446655440000)"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Outcomes) != 0 {
		t.Fatalf("render-only mention triggered work: %+v", plan.Outcomes)
	}
}
func TestInvalidMention(t *testing.T) {
	if _, err := Parse(`[@x](mention://all/nope)`); err == nil {
		t.Fatal("expected invalid all mention")
	}
}

func TestInvalidMentionMarkerCannotBeHiddenByAnotherValidMention(t *testing.T) {
	const id = "550e8400-e29b-41d4-a716-446655440000"
	content := `[@ok](mention://agent/` + id + `) mention://agent/not-a-markdown-link`
	if _, err := Parse(content); err == nil {
		t.Fatal("expected malformed structured mention marker to fail closed")
	}
}
func TestTriggerOutcomes(t *testing.T) {
	const id = "550e8400-e29b-41d4-a716-446655440000"
	p, err := ComputeTriggers(nil, TriggerInput{WorkspaceID: "w", CommentID: "c", CommentRevision: 2, Content: `[@x](mention://agent/` + id + `)`, Targets: []Target{{Type: TargetAgent, ID: id, WorkspaceID: "w", Active: true, CanInvoke: true}}, UserCanInvoke: true, RuntimeHealthy: true, PlanVersion: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Outcomes) != 1 || p.Outcomes[0].Status != StatusQueued {
		t.Fatalf("outcomes=%#v", p.Outcomes)
	}
	p, err = ComputeTriggers(nil, TriggerInput{WorkspaceID: "w", CommentID: "c", CommentRevision: 2, Content: `[@x](mention://agent/` + id + `)`, Targets: []Target{{Type: TargetAgent, ID: id, WorkspaceID: "w", Active: true}}, RuntimeHealthy: false})
	if err != nil {
		t.Fatal(err)
	}
	if p.Outcomes[0].Status != StatusBlocked {
		t.Fatalf("want blocked permission, got %#v", p.Outcomes[0])
	}
}

func TestTargetPermissionIsRequiredEvenForPrivilegedCaller(t *testing.T) {
	const id = "550e8400-e29b-41d4-a716-446655440000"
	plan, err := ComputeTriggers(nil, TriggerInput{WorkspaceID: "w", CommentID: "c", Content: `[@x](mention://agent/` + id + `)`, Targets: []Target{{Type: TargetAgent, ID: id, WorkspaceID: "w", Active: true, CanInvoke: false}}, UserCanInvoke: true, RuntimeHealthy: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Outcomes) != 1 || plan.Outcomes[0].ReasonCode != "invoke_forbidden" {
		t.Fatalf("outcomes=%#v", plan.Outcomes)
	}
}

func TestTriggerTargetLimitBlocksEveryTarget(t *testing.T) {
	content := ""
	targets := make([]Target, 0, MaxTargetsPerComment+1)
	for i := 0; i < MaxTargetsPerComment+1; i++ {
		id := fmt.Sprintf("550e8400-e29b-41d4-a716-44665544%04x", i)
		content += fmt.Sprintf("[@a%d](mention://agent/%s) ", i, id)
		targets = append(targets, Target{Type: TargetAgent, ID: id, WorkspaceID: "w", Active: true, CanInvoke: true})
	}
	plan, err := ComputeTriggers(nil, TriggerInput{WorkspaceID: "w", CommentID: "c-limit", Content: content, Targets: targets, UserCanInvoke: true, RuntimeHealthy: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Outcomes) != MaxTargetsPerComment+1 {
		t.Fatalf("outcomes=%d", len(plan.Outcomes))
	}
	for _, outcome := range plan.Outcomes {
		if outcome.Status != StatusBlocked || outcome.ReasonCode != "mention_limit_exceeded" {
			t.Fatalf("unblocked over-limit outcome=%+v", outcome)
		}
	}
}
