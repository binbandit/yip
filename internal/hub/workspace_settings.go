package hub

import (
	"context"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
)

// Workspace configuration belongs to the local owner, including in child
// workspaces that share their installation's session hub.
func (h *Hub) requireWorkspaceOwner(ctx context.Context, q store.Q, userID string) error {
	owner, err := h.ownerID(ctx, q)
	if err != nil {
		return err
	}
	if userID != owner {
		return domain.Forbidden("Only the workspace owner can change these settings.")
	}
	return nil
}
