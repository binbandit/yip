package integration

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	manifest "github.com/binbandit/yip/internal/context"
	"github.com/binbandit/yip/internal/providers/fake"
	"github.com/binbandit/yip/protocol"
)

// The owner renames themselves: the handle and sign-in are unchanged, open
// windows hear about it, and engineers use the new name from their next run.
func TestOwnerRename(t *testing.T) {
	var mu sync.Mutex
	var instructions string
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		if replyTo(m, "Security", "who am I") {
			mu.Lock()
			instructions = m.Instructions()
			mu.Unlock()
			return script(fake.Step{Final: "You're " + m.OwnerName + "."})
		}
		return nil
	}})
	cursor := e.c.boot.Cursor

	var u protocol.User
	e.c.must("PATCH", "/v1/profile", protocol.ProfileRequest{Name: "  Brayden \n Moon "}, &u)
	if u.Name != "Brayden Moon" || u.Handle != "brayden" {
		t.Fatalf("renamed to %q (@%s)", u.Name, u.Handle)
	}
	var boot protocol.Bootstrap
	e.c.must("GET", "/v1/bootstrap", nil, &boot)
	if boot.User.Name != "Brayden Moon" {
		t.Fatalf("bootstrap still says %q", boot.User.Name)
	}
	_, types, _ := e.sseEvents(strconv.FormatInt(cursor, 10), 50, 2*time.Second)
	if !strings.Contains(strings.Join(types, ","), "user.updated") {
		t.Fatalf("other windows weren't told about the rename: %v", types)
	}

	for _, bad := range []string{" \t ", strings.Repeat("x", 81)} {
		if err := e.c.do("PATCH", "/v1/profile", protocol.ProfileRequest{Name: bad}, nil); !isStatus(err, 400) {
			t.Fatalf("name %q: want 400, got %v", bad, err)
		}
	}
	e.c.must("GET", "/v1/bootstrap", nil, &boot)
	if boot.User.Name != "Brayden Moon" {
		t.Fatalf("a refused rename changed the name to %q", boot.User.Name)
	}
	// The handle still signs in.
	e.signIn()

	e.post("Security", "@Mira who am I?", []string{"mira"}, nil)
	e.waitMessage("Security", "You're Brayden Moon.")
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(instructions, "on Brayden Moon's team") {
		t.Fatalf("engineer instructions don't use the new name:\n%s", instructions)
	}
}
