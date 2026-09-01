package store

import (
	"testing"

	"github.com/adro-project/adro/internal/domain"
)

func TestWorkflowAndChatPersistAcrossMemoryRestart(t *testing.T) {
	path := t.TempDir() + "/control.json"
	first, err := NewPersistentMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	template, err := first.UpsertWorkflowTemplate(domain.WorkflowTemplate{WorkspaceID: "w", Name: "minimal", Mode: domain.WorkflowAutomatic, Steps: []domain.WorkflowStep{{ID: "dev", Stage: domain.PipelineDevelopment, AgentID: "dev", Required: true}, {ID: "report", Stage: domain.PipelineReport, AgentID: "report", Required: true}}})
	if err != nil {
		t.Fatal(err)
	}
	chat, err := first.CreateChatSession(domain.ChatSession{WorkspaceID: "w", ProjectID: "p", Title: "persisted", HarnessSessionID: "chat-persisted"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.AppendChatMessage(domain.ChatMessage{ChatSessionID: chat.ID, WorkspaceID: "w", Role: "user", Content: "durable"}); err != nil {
		t.Fatal(err)
	}
	second, err := NewPersistentMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	loadedTemplate, err := second.GetWorkflowTemplate(template.ID)
	if err != nil || len(loadedTemplate.Steps) != 2 {
		t.Fatalf("template=%+v err=%v", loadedTemplate, err)
	}
	loadedChat, err := second.GetChatSession(chat.ID)
	if err != nil || loadedChat.ProjectID != "p" {
		t.Fatalf("chat=%+v err=%v", loadedChat, err)
	}
	messages, err := second.ListChatMessages(chat.ID)
	if err != nil || len(messages) != 1 || messages[0].Content != "durable" {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
}
