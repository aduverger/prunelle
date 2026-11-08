package sessions

import (
	"encoding/json"
	"time"
)

// These types are the API wire format; api/openapi.yaml maps its schemas to them.

type SessionBackend struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Kind         string          `json:"kind"`
	Enabled      bool            `json:"enabled"`
	Capabilities []string        `json:"capabilities"`
	Config       json.RawMessage `json:"config"`
	Health       BackendHealth   `json:"health"`
}

// Backend health states.
const (
	HealthConnecting  = "CONNECTING"
	HealthReachable   = "REACHABLE"
	HealthUnreachable = "UNREACHABLE"
	HealthDisabled    = "DISABLED"
)

type BackendHealth struct {
	State     string            `json:"state"`
	Error     *string           `json:"error"`
	CheckedAt *time.Time        `json:"checked_at"`
	Info      map[string]string `json:"info"`
}

type RuntimeSession struct {
	ID                    string          `json:"id"`
	BackendID             string          `json:"backend_id"`
	BackendKind           string          `json:"backend_kind"`
	ExternalID            string          `json:"external_id"`
	State                 string          `json:"state"`
	Label                 string          `json:"label"`
	Cwd                   *string         `json:"cwd"`
	Process               *string         `json:"process"`
	Local                 bool            `json:"local"`
	HarnessHint           *string         `json:"harness_hint"`
	Capabilities          []string        `json:"capabilities"`
	Metadata              json.RawMessage `json:"metadata"`
	WorkspaceIDs          []string        `json:"workspace_ids"`
	SuggestedWorkspaceIDs []string        `json:"suggested_workspace_ids"`
	FirstSeenAt           time.Time       `json:"first_seen_at"`
	LastSeenAt            time.Time       `json:"last_seen_at"`
	StateChangedAt        time.Time       `json:"state_changed_at"`
}
