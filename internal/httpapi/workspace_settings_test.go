package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

func TestWorkspaceSettingsPermissionsAndPersistence(t *testing.T) {
	ctx := context.Background()
	cfg := hub.Config{DataDir: t.TempDir()}
	h, err := hub.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { h.Close() }()
	secret, _, err := h.IssueBootstrapSecret(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := h.Setup(ctx, protocol.SetupRequest{BootstrapSecret: secret, OrgName: "Settings", Name: "Owner", Handle: "owner", Password: "test-settings-password"})
	if err != nil {
		t.Fatal(err)
	}
	token, sess, err := h.SignIn(ctx, "owner", "test-settings-password", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	eng, err := h.CreateEngineer(ctx, owner.ID, protocol.CreateEngineerRequest{Name: "Mira", Role: "Engineer", Provider: protocol.ProviderPreference{Provider: "codex", Model: "chosen-model", Alternatives: []string{"pi"}}})
	if err != nil {
		t.Fatal(err)
	}
	// A synthetic second user verifies permissions independently of the UI.
	other := protocol.User{ID: domain.NewID(), OrgID: owner.OrgID, Name: "Viewer", Handle: "viewer", CreatedAt: owner.CreatedAt.Add(time.Second)}
	otherToken := domain.RandomToken(32)
	otherSession := store.Session{ID: domain.HashToken(otherToken), UserID: other.ID, CSRFToken: "viewer-csrf", CreatedAt: time.Now(), LastSeenAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	err = h.Store().Tx(ctx, func(tx *sql.Tx) error {
		if err := store.InsertUser(ctx, tx, store.UserRow{User: other}); err != nil {
			return err
		}
		if err := store.InsertSession(ctx, tx, otherSession, "test"); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO provider_profiles(id, org_id, provider, label, billing, max_concurrency, created_at) VALUES ('api-account', ?, 'codex', 'Synthetic API account', 'api', 1, ?)`, owner.OrgID, time.Now().UTC().Format(time.RFC3339Nano))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	s := New(h, Options{})
	request := func(method, path string, body any, cookie, csrf, origin string) *httptest.ResponseRecorder {
		t.Helper()
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "http://hub"+path, strings.NewReader(string(data)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		r.Header.Set(csrfHeader, csrf)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: s.sessionCookieName(), Value: cookie})
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	check := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body.String())
		}
	}
	bootstrap := func(cookie string) protocol.Bootstrap {
		t.Helper()
		w := request("GET", "/v1/bootstrap", nil, cookie, "", "")
		check(w, 200)
		var b protocol.Bootstrap
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	if !bootstrap(token).CanManageWorkspace || bootstrap(otherToken).CanManageWorkspace {
		t.Fatal("owner capability is wrong")
	}
	if bootstrap(token).Engineers[0].Provider.AllowAPIBilling {
		t.Fatal("billing enabled by default")
	}
	preference := eng.Provider
	preference.AllowAPIBilling = true
	update := protocol.UpdateEngineerRequest{Version: eng.Version, Provider: &preference}
	path := "/v1/engineers/" + eng.ID
	check(request("PATCH", path, update, "", "", "http://hub"), 401)
	check(request("PATCH", path, update, token, "", "http://hub"), 403)
	check(request("PATCH", path, update, token, sess.CSRFToken, "https://elsewhere.invalid"), 403)
	check(request("PATCH", path, update, otherToken, otherSession.CSRFToken, "http://hub"), 403)
	check(request("POST", "/v1/engineers", protocol.CreateEngineerRequest{Name: "Paid", Role: "Engineer", Provider: preference}, otherToken, otherSession.CSRFToken, "http://hub"), 403)
	check(request("PUT", "/v1/provider-profiles/api-account", protocol.ProviderProfileRequest{MaxConcurrency: 3}, otherToken, otherSession.CSRFToken, "http://hub"), 403)
	if bootstrap(token).Engineers[0].Provider.AllowAPIBilling {
		t.Fatal("denied request changed permission")
	}
	check(request("PATCH", path, update, token, sess.CSRFToken, "http://hub"), 200)
	check(request("PATCH", path, update, token, sess.CSRFToken, "http://hub"), 409)
	for _, max := range []int{0, 17} {
		check(request("PUT", "/v1/provider-profiles/api-account", protocol.ProviderProfileRequest{MaxConcurrency: max}, token, sess.CSRFToken, "http://hub"), 400)
	}
	check(request("PUT", "/v1/provider-profiles/api-account", protocol.ProviderProfileRequest{MaxConcurrency: 3}, token, sess.CSRFToken, "http://hub"), 200)
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	h, err = hub.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	s = New(h, Options{})
	persisted := bootstrap(token).Engineers[0]
	if !persisted.Provider.AllowAPIBilling || persisted.Provider.Model != "chosen-model" || fmt.Sprint(persisted.Provider.Alternatives) != "[pi]" {
		t.Fatalf("saved provider changed: %+v", persisted.Provider)
	}
	accounts, err := h.ProviderProfiles(ctx)
	if err != nil || len(accounts) != 1 || accounts[0].MaxConcurrency != 3 {
		t.Fatalf("accounts: %+v %v", accounts, err)
	}
	preference = persisted.Provider
	preference.AllowAPIBilling = false
	check(request("PATCH", path, protocol.UpdateEngineerRequest{Version: persisted.Version, Provider: &preference}, token, sess.CSRFToken, "http://hub"), 200)
	if bootstrap(token).Engineers[0].Provider.AllowAPIBilling {
		t.Fatal("revocation not saved")
	}
}
