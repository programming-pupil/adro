package store

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/adro-project/adro/internal/domain"
)

func (m *Memory) UpsertWorkflowTemplate(template domain.WorkflowTemplate) (domain.WorkflowTemplate, error) {
	if template.Mode == "" {
		template.Mode = domain.WorkflowAutomatic
	}
	if err := template.Validate(); err != nil {
		return domain.WorkflowTemplate{}, err
	}
	template.Name = strings.TrimSpace(template.Name)
	template.Mode = domain.WorkflowMode(strings.ToLower(strings.TrimSpace(string(template.Mode))))
	template.Steps = domain.NormalizeWorkflow(template.Steps)
	m.mu.Lock()
	defer m.mu.Unlock()
	if template.ID == "" {
		template.ID = domain.NewID()
	}
	now := time.Now().UTC()
	previous, existed := m.workflowTemplates[template.ID]
	if existed {
		template.CreatedAt = previous.CreatedAt
		template.Version = previous.Version + 1
	} else {
		template.CreatedAt = now
		template.Version = 1
	}
	template.UpdatedAt = now
	for id, existing := range m.workflowTemplates {
		if id != template.ID && existing.WorkspaceID == template.WorkspaceID && strings.EqualFold(existing.Name, template.Name) {
			return domain.WorkflowTemplate{}, fmt.Errorf("workflow template %q already exists", template.Name)
		}
	}
	m.workflowTemplates[template.ID] = template
	if err := m.persistLocked(); err != nil {
		if existed {
			m.workflowTemplates[template.ID] = previous
		} else {
			delete(m.workflowTemplates, template.ID)
		}
		return domain.WorkflowTemplate{}, fmt.Errorf("persist workflow template: %w", err)
	}
	return template.Clone(), nil
}

func (m *Memory) GetWorkflowTemplate(id string) (domain.WorkflowTemplate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	template, ok := m.workflowTemplates[id]
	if !ok {
		return domain.WorkflowTemplate{}, ErrNotFound
	}
	return template.Clone(), nil
}

func (m *Memory) ListWorkflowTemplates(workspaceID string) []domain.WorkflowTemplate {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]domain.WorkflowTemplate, 0, len(m.workflowTemplates))
	for _, item := range m.workflowTemplates {
		if workspaceID == "" || item.WorkspaceID == workspaceID {
			items = append(items, item.Clone())
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func (m *Memory) DeleteWorkflowTemplate(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous, ok := m.workflowTemplates[id]
	if !ok {
		return ErrNotFound
	}
	delete(m.workflowTemplates, id)
	if err := m.persistLocked(); err != nil {
		m.workflowTemplates[id] = previous
		return fmt.Errorf("persist workflow template: %w", err)
	}
	return nil
}

func (m *Memory) CreateChatSession(session domain.ChatSession) (domain.ChatSession, error) {
	if err := session.Validate(); err != nil {
		return domain.ChatSession{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if session.ID == "" {
		session.ID = domain.NewID()
	}
	if session.HarnessSessionID == "" {
		session.HarnessSessionID = "chat-" + session.ID
	}
	if session.Status == "" {
		session.Status = "active"
	}
	now := time.Now().UTC()
	session.CreatedAt = now
	session.UpdatedAt = now
	if _, exists := m.chatSessions[session.ID]; exists {
		return domain.ChatSession{}, ErrConflict
	}
	m.chatSessions[session.ID] = session
	if err := m.persistLocked(); err != nil {
		delete(m.chatSessions, session.ID)
		return domain.ChatSession{}, fmt.Errorf("persist chat session: %w", err)
	}
	return session, nil
}

func (m *Memory) GetChatSession(id string) (domain.ChatSession, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session, ok := m.chatSessions[id]
	if !ok {
		return domain.ChatSession{}, ErrNotFound
	}
	return session, nil
}

func (m *Memory) ListChatSessions(workspaceID, projectID string) []domain.ChatSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]domain.ChatSession, 0, len(m.chatSessions))
	for _, item := range m.chatSessions {
		if (workspaceID == "" || item.WorkspaceID == workspaceID) && (projectID == "" || item.ProjectID == projectID) {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items
}

func (m *Memory) AppendChatMessage(message domain.ChatMessage) (domain.ChatMessage, error) {
	if err := message.Validate(); err != nil {
		return domain.ChatMessage{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.chatSessions[message.ChatSessionID]
	if !ok {
		return domain.ChatMessage{}, ErrNotFound
	}
	if session.WorkspaceID != message.WorkspaceID {
		return domain.ChatMessage{}, ErrConflict
	}
	if message.ID == "" {
		message.ID = domain.NewID()
	}
	message.Role = strings.ToLower(strings.TrimSpace(message.Role))
	message.Content = strings.TrimSpace(message.Content)
	message.CreatedAt = time.Now().UTC()
	for _, existing := range m.chatMessages[message.ChatSessionID] {
		if existing.ID == message.ID {
			return existing, nil
		}
	}
	m.chatMessages[message.ChatSessionID] = append(m.chatMessages[message.ChatSessionID], message)
	session.UpdatedAt = message.CreatedAt
	m.chatSessions[session.ID] = session
	if err := m.persistLocked(); err != nil {
		messages := m.chatMessages[message.ChatSessionID]
		m.chatMessages[message.ChatSessionID] = messages[:len(messages)-1]
		return domain.ChatMessage{}, fmt.Errorf("persist chat message: %w", err)
	}
	return message, nil
}

func (m *Memory) ListChatMessages(sessionID string) ([]domain.ChatMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.chatSessions[sessionID]; !ok {
		return nil, ErrNotFound
	}
	items := append([]domain.ChatMessage(nil), m.chatMessages[sessionID]...)
	return items, nil
}

func (m *Memory) DeleteChatSession(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.chatSessions[id]
	if !ok {
		return ErrNotFound
	}
	delete(m.chatSessions, id)
	delete(m.chatMessages, id)
	if err := m.persistLocked(); err != nil {
		m.chatSessions[id] = session
		return fmt.Errorf("persist chat session: %w", err)
	}
	return nil
}
