package hub

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/auth"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

const (
	sessionTTL      = 30 * 24 * time.Hour
	bootstrapTTL    = 30 * time.Minute
	settingInstance = "instance_id"
)

// NeedsSetup reports whether no owner exists yet.
func (h *Hub) NeedsSetup(ctx context.Context) (bool, error) {
	n, err := store.CountUsers(ctx, h.st.R())
	return n == 0, err
}

// IssueBootstrapSecret creates a short-lived, single-use secret for creating
// the owner. It is printed on the hub host only; there is no unauthenticated
// public setup route.
func (h *Hub) IssueBootstrapSecret(ctx context.Context) (string, time.Time, error) {
	secret := domain.RandomToken(18)
	expires := h.now().Add(bootstrapTTL)
	err := h.do(ctx, func(t *txn) error {
		return store.InsertBootstrapSecret(ctx, t.tx, domain.HashToken(secret), expires)
	})
	return secret, expires, err
}

// Setup creates the organisation and its owner. It consumes the bootstrap secret.
func (h *Hub) Setup(ctx context.Context, req protocol.SetupRequest) (protocol.User, error) {
	req.OrgName, req.Name = strings.TrimSpace(req.OrgName), strings.TrimSpace(req.Name)
	handle := auth.NormalizeHandle(req.Handle)
	if handle == "" {
		handle = domain.Handle(req.Name)
	}
	if req.OrgName == "" || req.Name == "" || handle == "" {
		return protocol.User{}, domain.Invalid("Workspace name, your name, and a handle are required.")
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		return protocol.User{}, domain.Invalid("%s", err.Error())
	}
	var user protocol.User
	err = h.do(ctx, func(t *txn) error {
		n, err := store.CountUsers(ctx, t.tx)
		if err != nil {
			return err
		}
		if n > 0 {
			return domain.Conflict("This workspace already has an owner. Sign in instead.")
		}
		ok, err := store.ConsumeBootstrapSecret(ctx, t.tx, domain.HashToken(strings.TrimSpace(req.BootstrapSecret)), h.now())
		if err != nil {
			return err
		}
		if !ok {
			return domain.Forbidden("That setup code is invalid or expired. Run `yip hub setup-code` on the hub machine for a new one.")
		}
		org := protocol.Org{ID: domain.NewID(), Name: req.OrgName, CreatedAt: h.now()}
		if err := store.InsertOrg(ctx, t.tx, org); err != nil {
			return err
		}
		h.setOrg(org)
		user = protocol.User{ID: domain.NewID(), OrgID: org.ID, Name: req.Name, Handle: handle, CreatedAt: h.now()}
		if err := store.InsertUser(ctx, t.tx, store.UserRow{User: user, PasswordHash: hash,
			Preferences: protocol.Preferences{Theme: "system", Density: "comfortable", SendKey: "enter", Notify: "mentions"}}); err != nil {
			return err
		}
		if err := store.SetSetting(ctx, t.tx, settingInstance, domain.NewID()); err != nil {
			return err
		}
		// The owner's personal Overview conversation.
		overview := protocol.Room{ID: domain.NewID(), OrgID: org.ID, Name: "Overview", Kind: protocol.RoomKindOverview,
			Purpose: "Ask where things stand across your projects.", Private: true, ReplyMode: protocol.ReplyModeQuiet, CreatedAt: h.now()}
		if err := store.InsertRoom(ctx, t.tx, overview); err != nil {
			return err
		}
		if _, err := store.AddMember(ctx, t.tx, overview.ID, protocol.Member{Kind: protocol.ActorUser, ID: user.ID}); err != nil {
			return err
		}
		if err := t.audit(protocol.Actor{Kind: protocol.ActorUser, ID: user.ID}, "bootstrap_secret", "owner.create", user.ID, "ok", ""); err != nil {
			return err
		}
		return t.emit(ev{Type: "org.created", Actor: protocol.Actor{Kind: protocol.ActorUser, ID: user.ID}, Payload: org})
	})
	return user, err
}

// SignIn verifies credentials and creates a session. The returned token is
// the cookie value; only its hash is stored.
func (h *Hub) SignIn(ctx context.Context, handle, password, clientKey, userAgent string) (token string, s store.Session, err error) {
	handle = auth.NormalizeHandle(handle)
	limitKey := clientKey + "|" + handle
	if !h.logins.Allow(limitKey) {
		return "", s, domain.Limit("Too many sign-in attempts. Wait a few minutes and try again.")
	}
	u, err := store.GetUserByHandle(ctx, h.st.R(), handle)
	if err != nil {
		// Spend comparable time to avoid a user-enumeration timing signal.
		_, _ = auth.VerifyPassword("$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g", password)
		return "", s, domain.Unauthorized("That handle and password don't match.")
	}
	ok, err := auth.VerifyPassword(u.PasswordHash, password)
	if err != nil || !ok {
		return "", s, domain.Unauthorized("That handle and password don't match.")
	}
	h.logins.Reset(limitKey)
	token = domain.RandomToken(32)
	now := h.now()
	s = store.Session{ID: domain.HashToken(token), UserID: u.ID, CSRFToken: domain.RandomToken(24), CreatedAt: now,
		ExpiresAt: now.Add(sessionTTL), LastSeenAt: now}
	err = h.do(ctx, func(t *txn) error {
		if err := store.InsertSession(ctx, t.tx, s, truncate(userAgent, 200)); err != nil {
			return err
		}
		return t.audit(protocol.Actor{Kind: protocol.ActorUser, ID: u.ID}, "password", "session.create", s.ID[:12], "ok", "")
	})
	return token, s, err
}

// Authenticate resolves a session cookie to its user.
func (h *Hub) Authenticate(ctx context.Context, token string) (store.UserRow, store.Session, error) {
	if token == "" {
		return store.UserRow{}, store.Session{}, domain.Unauthorized("Sign in to continue.")
	}
	s, err := store.GetSession(ctx, h.st.R(), domain.HashToken(token))
	if err != nil {
		return store.UserRow{}, store.Session{}, domain.Unauthorized("Your session has ended. Sign in again.")
	}
	now := h.now()
	if s.RevokedAt != nil || now.After(s.ExpiresAt) {
		return store.UserRow{}, store.Session{}, domain.Unauthorized("Your session has ended. Sign in again.")
	}
	u, err := store.GetUser(ctx, h.st.R(), s.UserID)
	if err != nil {
		return store.UserRow{}, store.Session{}, domain.Unauthorized("Your session has ended. Sign in again.")
	}
	if now.Sub(s.LastSeenAt) > time.Hour {
		_ = h.do(ctx, func(t *txn) error { return store.TouchSession(ctx, t.tx, s.ID, now, now.Add(sessionTTL)) })
	}
	return u, s, nil
}

// SignOut revokes a session.
func (h *Hub) SignOut(ctx context.Context, sessionID string) error {
	return h.do(ctx, func(t *txn) error { return store.RevokeSession(ctx, t.tx, sessionID) })
}

// ResetOwnerPassword is the host-access recovery path: it sets a new password
// and revokes every existing session.
func (h *Hub) ResetOwnerPassword(ctx context.Context, handle, password string) error {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return domain.Invalid("%s", err.Error())
	}
	return h.do(ctx, func(t *txn) error {
		u, err := store.GetUserByHandle(ctx, t.tx, auth.NormalizeHandle(handle))
		if errors.Is(err, store.ErrNotFound) {
			return domain.NotFound("No user with handle %q.", handle)
		}
		if err != nil {
			return err
		}
		if err := store.SetUserPassword(ctx, t.tx, u.ID, hash); err != nil {
			return err
		}
		if err := store.RevokeUserSessions(ctx, t.tx, u.ID); err != nil {
			return err
		}
		return t.audit(protocol.Actor{Kind: protocol.ActorSystem, ID: "host"}, "host_access", "owner.password_reset", u.ID, "ok", "all sessions revoked")
	})
}

// SetPreferences stores the owner's UI preferences.
func (h *Hub) SetPreferences(ctx context.Context, userID string, p protocol.Preferences) error {
	return h.do(ctx, func(t *txn) error { return store.SetUserPreferences(ctx, t.tx, userID, p) })
}
