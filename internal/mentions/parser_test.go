package mentions

import "testing"

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
func TestInvalidMention(t *testing.T) {
	if _, err := Parse(`[@x](mention://all/nope)`); err == nil {
		t.Fatal("expected invalid all mention")
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
