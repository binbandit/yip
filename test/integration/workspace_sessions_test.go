package integration

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/binbandit/yip/protocol"
)

func TestWorkspacesOnSameHostKeepIndependentSessions(t *testing.T) {
	t.Parallel()
	work := newEnv(t, envOptions{})
	home := newEnv(t, envOptions{})
	// Browsers share cookies across ports on the same hostname.
	home.c.hc.Jar = work.c.hc.Jar
	home.c.must("POST", "/v1/session", protocol.SignInRequest{Handle: "brayden", Password: "correct-horse-battery"}, nil)
	var boot protocol.Bootstrap
	work.c.must("GET", "/v1/bootstrap", nil, &boot)
	home.c.must("GET", "/v1/bootstrap", nil, &boot)
	home.c.csrf = boot.CSRFToken
	home.c.must("DELETE", "/v1/session", nil, nil)
	work.c.must("GET", "/v1/bootstrap", nil, &boot)
	if err := home.c.do("GET", "/v1/bootstrap", nil, nil); err == nil {
		t.Fatal("signed-out workspace still authenticated")
	}
	work.c.must("GET", "/v1/bootstrap", nil, &boot)
}

func TestWorkspaceSessionMigratesLegacyCookie(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	u, _ := url.Parse(e.c.base)
	cookies := e.c.hc.Jar.Cookies(u)
	if len(cookies) != 1 {
		t.Fatalf("expected one session cookie, got %d", len(cookies))
	}
	current := cookies[0]
	e.c.hc.Jar.SetCookies(u, []*http.Cookie{
		{Name: current.Name, Path: "/", MaxAge: -1},
		{Name: "yip_session", Value: current.Value, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode},
	})
	e.c.must("GET", "/v1/bootstrap", nil, nil)
	migrated := e.c.hc.Jar.Cookies(u)
	if len(migrated) != 1 || migrated[0].Name == "yip_session" || migrated[0].Value != current.Value {
		t.Fatal("existing sign-in was not migrated without losing the session")
	}
}
