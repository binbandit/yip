package hub

import (
	"context"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

// SeedWorkspace creates an empty workspace with a local owner.
// Credentials and sessions remain exclusively in the installation's root hub.
func (h *Hub) SeedWorkspace(ctx context.Context, name string, owner protocol.User) error {
	if h.cfg.SessionHub == nil {
		return domain.Forbidden("Workspace seeding requires a session hub.")
	}
	org := protocol.Org{ID: domain.NewID(), Name: name, CreatedAt: h.now()}
	err := h.do(ctx, func(t *txn) error {
		n, err := store.CountUsers(ctx, t.tx)
		if err != nil {
			return err
		}
		if n != 0 {
			return domain.Conflict("Workspace already initialized.")
		}
		if err := store.InsertOrg(ctx, t.tx, org); err != nil {
			return err
		}
		owner.OrgID = org.ID
		owner.AvatarID = ""
		if err := store.InsertUser(ctx, t.tx, store.UserRow{User: owner, PasswordHash: "",
			Preferences: protocol.Preferences{Theme: "system", Density: "comfortable", SendKey: "enter", Notify: "mentions"}}); err != nil {
			return err
		}
		if err := store.SetSetting(ctx, t.tx, settingInstance, domain.NewID()); err != nil {
			return err
		}
		return nil
	})
	if err == nil {
		h.setOrg(org)
	}
	return err
}
