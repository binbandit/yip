package protocol

// Workspace identifies an independent workspace within this installation.
type Workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type CreateWorkspaceRequest struct {
	Name string `json:"name"`
}
