package protocol

import "time"

// EngineerDraftFields are suggestions only; identity and provider remain owner choices.
type EngineerDraftFields struct {
	Role           string   `json:"role"`
	Description    string   `json:"description"`
	CapabilityTags []string `json:"capabilityTags"`
	Instructions   string   `json:"instructions"`
}

type CreateEngineerDraftRequest struct {
	Description string             `json:"description"`
	NodeID      string             `json:"nodeId"`
	Provider    ProviderPreference `json:"provider"`
}

// EngineerDraft uses the runner protocol without creating an engineer or a room job.
type EngineerDraft struct {
	ID        string               `json:"id"`
	NodeID    string               `json:"nodeId"`
	Provider  ProviderPreference   `json:"provider"`
	State     RunState             `json:"state"`
	Detail    string               `json:"detail"`
	Fields    *EngineerDraftFields `json:"fields,omitempty"`
	CreatedAt time.Time            `json:"createdAt"`
}
