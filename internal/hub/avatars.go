package hub

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/gif" // registers the formats image.DecodeConfig checks
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

// MaxAvatarBytes bounds a profile picture upload. Animated GIFs are the large
// case; photos and stills are far smaller.
const MaxAvatarBytes = 10 << 20

// maxAvatarSide bounds a picture's width and height, so a small file can't
// ask every browser showing it for an enormous decode.
const maxAvatarSide = 8192

// avatarTypes are the accepted picture formats (as sniffed from the bytes)
// and the extension each is named with. SVG is never accepted: it can carry
// script.
var avatarTypes = map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/gif": "gif", "image/webp": "webp"}

// readAvatar reads and checks an uploaded picture, returning its bytes and
// content type. The format comes from the bytes, never from what the client
// declared.
func readAvatar(r io.Reader) ([]byte, string, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxAvatarBytes+1))
	if err != nil {
		return nil, "", domain.Invalid("The picture didn't finish uploading. Try again.")
	}
	if len(data) > MaxAvatarBytes {
		return nil, "", domain.Invalid("Choose a picture under %d MB.", MaxAvatarBytes>>20)
	}
	ct := http.DetectContentType(data)
	if _, ok := avatarTypes[ct]; !ok || len(data) == 0 {
		return nil, "", domain.Invalid("Choose a PNG, JPEG, GIF or WebP picture.")
	}
	// The standard library can't read WebP; the format itself caps each side
	// at 16,383 pixels.
	if ct != "image/webp" {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return nil, "", domain.Invalid("That picture can't be read. It may be damaged; try another.")
		}
		if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > maxAvatarSide || cfg.Height > maxAvatarSide {
			return nil, "", domain.Invalid("Choose a picture no more than %d pixels on each side.", maxAvatarSide)
		}
	}
	return data, ct, nil
}

// putAvatar checks a picture and writes its content to the artifact store,
// returning the artifact to record (in the caller's transaction). A nil
// picture returns a zero artifact: the picture is being removed.
func (h *Hub) putAvatar(picture io.Reader, name string) (protocol.Artifact, error) {
	if picture == nil {
		return protocol.Artifact{}, nil
	}
	data, ct, err := readAvatar(picture)
	if err != nil {
		return protocol.Artifact{}, err
	}
	hash, size, err := h.artifacts.PutStream(bytes.NewReader(data), MaxAvatarBytes)
	if err != nil {
		return protocol.Artifact{}, err
	}
	return protocol.Artifact{ID: domain.NewID(), Hash: hash, Name: name + "." + avatarTypes[ct], ContentType: ct, Size: size,
		Kind: "avatar", CreatedAt: h.now()}, nil
}

// recordAvatar inserts a stored picture's artifact, if there is one, and
// returns the ID to point at ("" to clear).
func (t *txn) recordAvatar(a protocol.Artifact) (string, error) {
	if a.ID == "" {
		return "", nil
	}
	return a.ID, store.InsertArtifact(t.ctx, t.tx, t.h.Org().ID, a)
}

// SetUserAvatar replaces the user's profile picture with the uploaded image
// (PNG, JPEG, GIF — animated too — or WebP). A nil picture removes it.
func (h *Hub) SetUserAvatar(ctx context.Context, userID string, picture io.Reader) (protocol.User, error) {
	var u store.UserRow
	cur, err := store.GetUser(ctx, h.st.R(), userID)
	if err != nil {
		return u.User, err
	}
	art, err := h.putAvatar(picture, cur.Handle)
	if err != nil {
		return u.User, err
	}
	err = h.do(ctx, func(t *txn) error {
		id, err := t.recordAvatar(art)
		if err != nil {
			return err
		}
		if _, err := store.SetUserAvatar(ctx, t.tx, userID, id); err != nil {
			return err
		}
		if u, err = store.GetUser(ctx, t.tx, userID); err != nil {
			return err
		}
		return t.emit(ev{Type: "user.updated", Actor: userActor(userID), Payload: u.User})
	})
	return u.User, err
}

// SetEngineerAvatar replaces an engineer's profile picture with the uploaded
// image. A nil picture removes it. Pictures aren't configuration: running
// work is unaffected and no new version is written.
func (h *Hub) SetEngineerAvatar(ctx context.Context, userID, engineerID string, picture io.Reader) (protocol.Engineer, error) {
	e, err := store.GetEngineer(ctx, h.st.R(), engineerID)
	if errors.Is(err, store.ErrNotFound) {
		return e, domain.NotFound("That engineer doesn't exist.")
	}
	if err != nil {
		return e, err
	}
	art, err := h.putAvatar(picture, e.Handle)
	if err != nil {
		return e, err
	}
	err = h.do(ctx, func(t *txn) error {
		id, err := t.recordAvatar(art)
		if err != nil {
			return err
		}
		ok, err := store.SetEngineerAvatar(ctx, t.tx, engineerID, id)
		if err != nil {
			return err
		}
		if !ok {
			return domain.NotFound("That engineer doesn't exist.")
		}
		if e, err = store.GetEngineer(ctx, t.tx, engineerID); err != nil {
			return err
		}
		return t.emit(ev{Type: "engineer.updated", Actor: userActor(userID), Payload: e})
	})
	return e, err
}

// OpenAvatar opens a profile picture for serving. Only avatar artifacts are
// served this way; everything else goes through OpenArtifact's access checks.
func (h *Hub) OpenAvatar(ctx context.Context, id string) (protocol.Artifact, *os.File, error) {
	a, err := store.GetArtifact(ctx, h.st.R(), id)
	if err != nil || a.Kind != "avatar" {
		return a, nil, domain.NotFound("That picture doesn't exist.")
	}
	f, err := h.artifacts.Open(a.Hash)
	if err != nil {
		return a, nil, domain.Unavailable("artifact_store", "The picture is missing from the hub's store.")
	}
	return a, f, nil
}
