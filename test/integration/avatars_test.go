package integration

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 200, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// animatedGIF is a two-frame animation.
func animatedGIF(t *testing.T) []byte {
	t.Helper()
	anim := &gif.GIF{}
	for i := range 2 {
		frame := image.NewPaletted(image.Rect(0, 0, 16, 16), palette.Plan9)
		frame.SetColorIndex(i, i, uint8(10+i*100))
		anim.Image = append(anim.Image, frame)
		anim.Delay = append(anim.Delay, 20)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, anim); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// fetch GETs a path and returns the response with its body read.
func (c *client) fetch(path string) (*http.Response, []byte) {
	c.t.Helper()
	resp, err := c.hc.Get(c.base + path)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

func TestProfilePictures(t *testing.T) {
	e := newEnv(t, envOptions{noRunner: true})
	anim := animatedGIF(t)

	// The owner's own picture: an animated GIF is kept byte for byte.
	var me protocol.User
	e.c.must("PUT", "/v1/profile/avatar", anim, &me)
	if me.AvatarID == "" {
		t.Fatal("uploading a picture didn't set the owner's avatar")
	}
	var boot protocol.Bootstrap
	e.c.must("GET", "/v1/bootstrap", nil, &boot)
	if boot.User.AvatarID != me.AvatarID {
		t.Fatalf("bootstrap user avatar = %q, want %q", boot.User.AvatarID, me.AvatarID)
	}
	resp, body := e.c.fetch("/v1/avatars/" + me.AvatarID)
	if resp.StatusCode != 200 || !bytes.Equal(body, anim) {
		t.Fatalf("serving the picture: HTTP %d, %d bytes (want the %d uploaded)", resp.StatusCode, len(body), len(anim))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/gif" {
		t.Fatalf("picture served as %q, want image/gif", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("a picture's content never changes under its ID, so it should cache for good; got %q", cc)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Fatalf("picture served without a sandbox CSP: %q", csp)
	}

	// An engineer's picture: set without a new configuration version.
	mira := e.engineerID("mira")
	var before struct{ Engineer protocol.Engineer }
	e.c.must("GET", "/v1/engineers/"+mira, nil, &before)
	var eng protocol.Engineer
	e.c.must("PUT", "/v1/engineers/"+mira+"/avatar", pngBytes(t, 64, 64), &eng)
	if eng.AvatarID == "" || eng.AvatarID == me.AvatarID {
		t.Fatalf("engineer avatar = %q", eng.AvatarID)
	}
	if eng.VersionNo != before.Engineer.VersionNo || eng.Version <= before.Engineer.Version {
		t.Fatalf("a picture must bump the row version (for live updates) but not the configuration version: before v%d/%d, after v%d/%d",
			before.Engineer.VersionNo, before.Engineer.Version, eng.VersionNo, eng.Version)
	}
	if resp, _ := e.c.fetch("/v1/avatars/" + eng.AvatarID); resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("engineer picture served as %q", resp.Header.Get("Content-Type"))
	}
	var stale protocol.Engineer
	if err := e.c.do("PATCH", "/v1/engineers/"+mira, protocol.UpdateEngineerRequest{Version: before.Engineer.Version}, &stale); !isStatus(err, 409) {
		t.Fatalf("an edit based on the profile before the picture changed should conflict, got %v", err)
	}

	// Anything that isn't a readable PNG, JPEG, GIF or WebP is refused,
	// whatever it claims to be, and the current picture stays.
	bad := map[string][]byte{
		"svg (can carry script)": []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"html":                   []byte("<!doctype html><title>x</title>"),
		"damaged png":            append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...),
		"too many pixels":        pngBytes(t, 8193, 1),
		"too large":              append(append([]byte{}, anim...), make([]byte, hub.MaxAvatarBytes)...),
		"empty":                  {},
	}
	for name, b := range bad {
		if err := e.c.do("PUT", "/v1/profile/avatar", b, nil); !isStatus(err, 400) {
			t.Fatalf("%s: want 400, got %v", name, err)
		}
	}
	e.c.must("GET", "/v1/bootstrap", nil, &boot)
	if boot.User.AvatarID != me.AvatarID {
		t.Fatal("a refused upload changed the picture")
	}
	if err := e.c.do("PUT", "/v1/engineers/nobody/avatar", pngBytes(t, 8, 8), nil); !isStatus(err, 404) {
		t.Fatalf("unknown engineer: want 404, got %v", err)
	}

	// Only pictures are served here; other artifacts keep their own access checks.
	file := protocol.Artifact{ID: "art-file", Hash: strings.Repeat("a", 64), Name: "notes.txt", ContentType: "text/plain", Kind: "file"}
	if err := e.hub.Store().Tx(e.ctx, func(tx *sql.Tx) error { return store.InsertArtifact(e.ctx, tx, e.hub.Org().ID, file) }); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{file.ID, "missing"} {
		if resp, _ := e.c.fetch("/v1/avatars/" + id); resp.StatusCode != 404 {
			t.Fatalf("/v1/avatars/%s: want 404, got %d", id, resp.StatusCode)
		}
	}

	// The picture is part of your data: exports carry it.
	resp, body = e.c.fetch("/v1/export")
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("export: HTTP %d, %v", resp.StatusCode, err)
	}
	var exported bool
	for _, f := range zr.File {
		if f.Name == "artifacts/"+fmt.Sprintf("%x", sha256.Sum256(anim)) {
			exported = true
		}
	}
	if !exported {
		t.Fatal("the export doesn't include the owner's picture")
	}

	// Removing goes back to the initial; it survives a restart.
	var cleared protocol.User
	var clearedEng protocol.Engineer
	e.c.must("DELETE", "/v1/profile/avatar", nil, &cleared)
	e.c.must("DELETE", "/v1/engineers/"+mira+"/avatar", nil, &clearedEng)
	if cleared.ID != me.ID || cleared.AvatarID != "" || clearedEng.ID != mira || clearedEng.AvatarID != "" {
		t.Fatalf("removal left pictures: owner %+v, Mira %q", cleared, clearedEng.AvatarID)
	}
	e.c.must("PUT", "/v1/engineers/"+mira+"/avatar", anim, &eng)
	e.restartHub()
	var after struct{ Engineer protocol.Engineer }
	e.c.must("GET", "/v1/engineers/"+mira, nil, &after)
	if after.Engineer.AvatarID != eng.AvatarID || e.c.boot.User.AvatarID != "" {
		t.Fatalf("after restart: Mira %q (want %q), owner %q (want none)", after.Engineer.AvatarID, eng.AvatarID, e.c.boot.User.AvatarID)
	}
}
