package audit

import "testing"

func TestLedgerHashChain(t *testing.T) {
	l := NewLedger()
	if _, err := l.Append(Event{TenantID: "t", ActorID: "m", Action: "requirement.created"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(Event{TenantID: "t", ActorID: "agent", Action: "execution.started"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Verify(); err != nil {
		t.Fatal(err)
	}
}
