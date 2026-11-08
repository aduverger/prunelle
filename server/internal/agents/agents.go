// Package agents will hold harness adapters (Pi first, M4) that talk to coding-agent
// conversations semantically: history, messages, tool events. It is deliberately separate from
// package sessions, which only discovers and observes the processes that host agents.
package agents

import "context"

// AgentAdapter speaks one harness's semantic protocol. No implementation exists before M4.
type AgentAdapter interface {
	Kind() string
	GetHistory(ctx context.Context, session SessionRef) ([]Message, error)
	Send(ctx context.Context, session SessionRef, message Message) (InvocationRef, error)
	Subscribe(ctx context.Context, session SessionRef) (<-chan Event, error)
}

// SessionRef identifies a harness conversation, independent of any runtime session.
type SessionRef struct {
	HarnessKind string
	ExternalID  string
}

type Message struct {
	Role string
	Text string
	// ThreadID correlates replies with a review discussion; never rely on arrival order.
	ThreadID string
}

type InvocationRef struct {
	ID       string
	ThreadID string
}

type Event struct {
	Kind    string
	Message *Message
}
