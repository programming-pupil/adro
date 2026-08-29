package domain

import "testing"

func TestRequirementTransitions(t *testing.T) {
	valid := [][2]RequirementStatus{{RequirementReceived, RequirementTriaged}, {RequirementTriaged, RequirementAssigneesConfirmed}, {RequirementDesignReview, RequirementDeveloping}, {RequirementTesting, RequirementReadyForHumanQA}, {RequirementReadyForHumanQA, RequirementAccepted}}
	for _, pair := range valid {
		if err := Transition(pair[0], pair[1]); err != nil {
			t.Errorf("%s -> %s: %v", pair[0], pair[1], err)
		}
	}
	if err := Transition(RequirementReceived, RequirementReleased); err == nil {
		t.Fatal("expected invalid transition")
	}
}
func TestRequirementValidation(t *testing.T) {
	r := Requirement{WorkspaceID: "w", Title: "title", Description: "desc", AcceptanceCriteria: []string{"works"}, AssigneeMemberIDs: []string{"m"}}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
}
