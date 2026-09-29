package hub

import (
	"context"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

// SeedWorkspace creates only a local owner and their personal Overview.
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
		room := protocol.Room{ID: domain.NewID(), OrgID: org.ID, Name: "Overview", Kind: protocol.RoomKindOverview,
			Purpose: "Ask where things stand across your projects.", Private: true, ReplyMode: protocol.ReplyModeQuiet, CreatedAt: h.now()}
		if err := store.InsertRoom(ctx, t.tx, room); err != nil {
			return err
		}
		_, err = store.AddMember(ctx, t.tx, room.ID, protocol.Member{Kind: protocol.ActorUser, ID: owner.ID})
		return err
	})
	if err == nil {
		h.setOrg(org)
	}
	return err
}
