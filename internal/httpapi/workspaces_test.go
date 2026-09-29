package httpapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/binbandit/yip/internal/auth"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

func TestWorkspaces(t *testing.T) {
	ctx := context.Background()
	cfg := hub.Config{DataDir: t.TempDir(), RunnerURL: "https://localhost:7443"}
	root, err := hub.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := root.IssueBootstrapSecret(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := root.Setup(ctx, protocol.SetupRequest{BootstrapSecret: secret, OrgName: "Root", Name: "Owner", Handle: "owner", Password: "a-long-test-password"})
	if err != nil {
		t.Fatal(err)
	}
	token, session, err := root.SignIn(ctx, "owner", "a-long-test-password", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	root.Start(ctx)
	m, err := OpenWorkspaces(ctx, root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { m.Close() }()
	cookie := &http.Cookie{Name: m.root.browser.sessionCookieName(), Value: token}
	request := func(method, path, body, key string, signed, csrf bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://hub"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://hub")
		if key != "" {
			r.Header.Set(idemHeader, key)
		}
		if signed {
			r.AddCookie(cookie)
		}
		if csrf {
			r.Header.Set(csrfHeader, session.CSRFToken)
		}
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		return w
	}
	check := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body.String())
		}
	}
	check(request("GET", "/v1/workspaces", "", "", false, false), 401)
	check(request("POST", "/v1/workspaces", `{"name":"Second"}`, "", true, false), 403)
	badOrigin := httptest.NewRequest("POST", "http://hub/v1/workspaces", strings.NewReader(`{"name":"Second"}`))
	badOrigin.AddCookie(cookie)
	badOrigin.Header.Set(csrfHeader, session.CSRFToken)
	badOrigin.Header.Set("Origin", "https://untrusted.example")
	blocked := httptest.NewRecorder()
	m.ServeHTTP(blocked, badOrigin)
	check(blocked, 403)
	w := request("POST", "/v1/workspaces", `{"name":" Second "}`, "create-second", true, true)
	check(w, 201)
	var workspace protocol.Workspace
	if err := json.Unmarshal(w.Body.Bytes(), &workspace); err != nil {
		t.Fatal(err)
	}
	if workspace.Name != "Second" || workspace.Path != "/w/"+workspace.ID {
		t.Fatal(workspace)
	}
	for _, handler := range []http.Handler{m, m.RunnerHandler()} {
		for _, method := range []string{"GET", "POST"} {
			r := httptest.NewRequest(method, "http://hub"+workspace.Path+"?keep=query", nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			check(w, http.StatusTemporaryRedirect)
			if got := w.Header().Get("Location"); got != workspace.Path+"/?keep=query" {
				t.Fatalf("bare workspace redirect escaped: %q", got)
			}
			for _, suffix := range []string{"//v1/bootstrap", "/a/../v1/bootstrap", "/./v1/bootstrap", "/%2e%2e/v1/bootstrap"} {
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, httptest.NewRequest(method, "http://hub"+workspace.Path+suffix, nil))
				check(w, http.StatusBadRequest)
				if location := w.Header().Get("Location"); location != "" {
					t.Fatalf("ambiguous child path redirected: %q", location)
				}
			}
		}
	}
	check(request("POST", "/v1/workspaces", `{"name":"second"}`, "", true, true), 409)
	for _, name := range []string{"", "  ", strings.Repeat("a", 81), "bad\nname"} {
		b, _ := json.Marshal(protocol.CreateWorkspaceRequest{Name: name})
		check(request("POST", "/v1/workspaces", string(b), "", true, true), 400)
	}
	replay := request("POST", workspace.Path+"/v1/workspaces", `{"name":" Second "}`, "create-second", true, true)
	check(replay, 201)
	if replay.Header().Get("Idempotent-Replayed") != "true" || replay.Body.String() != w.Body.String() {
		t.Fatal("creation not replayed across prefixes")
	}
	check(request("GET", "/w/unknown/v1/bootstrap", "", "", true, false), 404)
	child := m.children[workspace.ID].hub
	signIn := request("POST", workspace.Path+"/v1/session", `{"handle":"owner","password":"a-long-test-password"}`, "", false, false)
	check(signIn, 200)
	if cookies := signIn.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != cookie.Name || cookies[0].Path != "/" {
		t.Fatal("child sign-in did not use installation cookie")
	}
	if child.CA() != root.CA() || child.Config().RunnerURL != cfg.RunnerURL+workspace.Path {
		t.Fatal("runner trust or URL")
	}
	local, err := store.GetUser(ctx, child.Store().R(), owner.ID)
	if err != nil || local.PasswordHash != "" || local.OrgID == owner.OrgID {
		t.Fatalf("local owner: %+v %v", local, err)
	}
	readBootstrap := func(path string) protocol.Bootstrap {
		t.Helper()
		w := request("GET", path+"/v1/bootstrap", "", "", true, false)
		check(w, 200)
		var b protocol.Bootstrap
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	b := readBootstrap(workspace.Path)
	if len(b.Rooms) != 0 || len(b.Engineers)+len(b.Projects)+len(b.Nodes) != 0 || b.User.ID != owner.ID {
		t.Fatalf("fresh bootstrap: %+v", b)
	}
	childChanged := child.Bus().Changed()
	check(request("POST", "/v1/engineers", `{"name":"Root Engineer","role":"Developer","provider":{"provider":"codex"}}`, "", true, true), 201)
	check(request("POST", "/v1/projects", `{"name":"Root Project"}`, "", true, true), 201)
	created := request("POST", "/v1/rooms", `{"name":"Root room"}`, "", true, true)
	check(created, 201)
	var rootRoom protocol.Room
	if err := json.Unmarshal(created.Body.Bytes(), &rootRoom); err != nil {
		t.Fatal(err)
	}
	room := rootRoom.ID
	check(request("POST", "/v1/rooms/"+room+"/messages", `{"body":"root-secret-needle","clientKey":"root-message"}`, "", true, true), 201)
	select {
	case <-childChanged:
		t.Fatal("root mutation notified child bus")
	default:
	}
	check(request("GET", workspace.Path+"/v1/rooms/"+room+"/messages", "", "", true, false), 404)
	b = readBootstrap(workspace.Path)
	if len(b.Engineers)+len(b.Projects) != 0 {
		t.Fatal("root resources leaked")
	}
	rootBootstrap := readBootstrap("")
	check(request("GET", workspace.Path+"/v1/engineers/"+rootBootstrap.Engineers[0].ID, "", "", true, false), 404)
	check(request("GET", workspace.Path+"/v1/projects/"+rootBootstrap.Projects[0].ID, "", "", true, false), 404)
	w = request("GET", workspace.Path+"/v1/search?q=root-secret-needle", "", "", true, false)
	check(w, 200)
	if strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("search leaked: %s", w.Body.String())
	}
	if err := child.SetPreferences(ctx, owner.ID, protocol.Preferences{Theme: "night"}); err != nil {
		t.Fatal(err)
	}
	if readBootstrap("").Preferences.Theme == "night" {
		t.Fatal("preferences leaked")
	}

	// Shared TLS trust must never imply cross-workspace machine authority.
	_, csr, err := auth.NewNodeKeyAndCSR("test")
	if err != nil {
		t.Fatal(err)
	}
	en, err := child.CreateEnrollment(ctx, owner.ID, protocol.CreateEnrollmentRequest{Name: "Child runner"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := root.Pair(ctx, protocol.PairRequest{Token: en.Token, CSRPEM: string(csr)}); err == nil {
		t.Fatal("root accepted child enrollment")
	}
	pair, err := child.Pair(ctx, protocol.PairRequest{Token: en.Token, CSRPEM: string(csr)})
	if err != nil {
		t.Fatal(err)
	}
	cert, err := auth.ParseCertPEM([]byte(pair.CertPEM))
	if err != nil {
		t.Fatal(err)
	}
	id, serial := auth.NodeIDFromCert(cert)
	if err := child.AuthorizeNode(ctx, id, serial); err != nil {
		t.Fatal(err)
	}
	if err := root.AuthorizeNode(ctx, id, serial); err == nil {
		t.Fatal("root accepted child machine")
	}
	for _, path := range []string{"/v1/runner/artifacts/missing", "/w/unknown/v1/runner/artifacts/missing"} {
		r := httptest.NewRequest("GET", "https://hub"+path, nil)
		r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}, VerifiedChains: [][]*x509.Certificate{{cert, root.CA().Cert}}}
		w := httptest.NewRecorder()
		m.RunnerHandler().ServeHTTP(w, r)
		want := 401
		if strings.HasPrefix(path, "/w/unknown/") {
			want = 404
		}
		check(w, want)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	root, err = hub.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	root.Start(ctx)
	m, err = OpenWorkspaces(ctx, root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	w = request("GET", workspace.Path+"/v1/workspaces", "", "", true, false)
	check(w, 200)
	var listed []protocol.Workspace
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].Path != "" || listed[1] != workspace {
		t.Fatalf("restart list: %+v", listed)
	}
	if readBootstrap(workspace.Path).Preferences.Theme != "night" {
		t.Fatal("child state lost on restart")
	}
	check(request("DELETE", workspace.Path+"/v1/session", "", "", true, true), 200)
	if root.SessionActive(ctx, session.ID) {
		t.Fatal("child sign-out did not revoke root session")
	}
	check(request("GET", "/v1/bootstrap", "", "", true, false), 401)
	check(request("GET", workspace.Path+"/v1/bootstrap", "", "", true, false), 401)
}
