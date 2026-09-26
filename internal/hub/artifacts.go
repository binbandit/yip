package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MaxArtifactBytes bounds a single artifact upload.
const MaxArtifactBytes = 512 << 20

// ArtifactStore keeps content-addressed files on the hub's local disk.
type ArtifactStore struct{ dir string }

func NewArtifactStore(dir string) (*ArtifactStore, error) {
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o700); err != nil {
		return nil, err
	}
	return &ArtifactStore{dir: dir}, nil
}

// Dir returns the artifact directory.
func (a *ArtifactStore) Dir() string { return a.dir }

func validHash(h string) bool {
	if len(h) != 64 {
		return false
	}
	_, err := hex.DecodeString(h)
	return err == nil
}

// Path returns where an artifact with the given sha256 hex lives.
func (a *ArtifactStore) Path(hash string) (string, error) {
	if !validHash(hash) {
		return "", errors.New("invalid artifact hash")
	}
	return filepath.Join(a.dir, hash[:2], hash), nil
}

// Has reports whether content with this hash is stored.
func (a *ArtifactStore) Has(hash string) bool {
	p, err := a.Path(hash)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// Put streams content to a temporary file, verifies the declared size and
// SHA-256, and atomically moves it into place. Nothing is visible under the
// hash until verification succeeds.
func (a *ArtifactStore) Put(r io.Reader, declaredHash string, declaredSize int64) error {
	declaredHash = strings.ToLower(declaredHash)
	if !validHash(declaredHash) {
		return errors.New("declared hash must be sha256 hex")
	}
	if declaredSize < 0 || declaredSize > MaxArtifactBytes {
		return fmt.Errorf("artifact size %d outside allowed range", declaredSize)
	}
	if a.Has(declaredHash) {
		_, _ = io.Copy(io.Discard, io.LimitReader(r, declaredSize))
		return nil
	}
	tmp, got, n, err := a.spool(r, declaredSize+1)
	defer os.Remove(tmp)
	if err != nil {
		return err
	}
	if n != declaredSize {
		return fmt.Errorf("artifact size mismatch: declared %d, received %d", declaredSize, n)
	}
	if got != declaredHash {
		return fmt.Errorf("artifact checksum mismatch: declared %s, received %s", declaredHash, got)
	}
	return a.place(tmp, declaredHash)
}

// PutStream stores content whose hash isn't known in advance (an owner's
// upload), hashing it as it streams, up to max bytes.
func (a *ArtifactStore) PutStream(r io.Reader, max int64) (hash string, size int64, err error) {
	tmp, hash, n, err := a.spool(r, max+1)
	defer os.Remove(tmp)
	if err != nil {
		return "", 0, err
	}
	if n > max {
		return "", 0, fmt.Errorf("the upload is larger than %d MB", max>>20)
	}
	if a.Has(hash) {
		return hash, n, nil
	}
	if err := a.place(tmp, hash); err != nil {
		return "", 0, err
	}
	return hash, n, nil
}

// spool copies up to limit bytes of r into a temporary file, hashing them as
// they stream. The caller removes the file.
func (a *ArtifactStore) spool(r io.Reader, limit int64) (path, hash string, n int64, err error) {
	tmp, err := os.CreateTemp(filepath.Join(a.dir, "tmp"), "upload-*")
	if err != nil {
		return "", "", 0, err
	}
	h := sha256.New()
	n, err = io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, limit))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	return tmp.Name(), hex.EncodeToString(h.Sum(nil)), n, err
}

// place moves a verified temporary file to its content-addressed path.
func (a *ArtifactStore) place(tmp, hash string) error {
	dest, _ := a.Path(hash)
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

// Open opens stored content for reading.
func (a *ArtifactStore) Open(hash string) (*os.File, error) {
	p, err := a.Path(hash)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

// Verify re-hashes stored content (used by restore checks).
func (a *ArtifactStore) Verify(hash string) error {
	f, err := a.Open(hash)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != hash {
		return fmt.Errorf("artifact %s is corrupt (hash %s)", hash, got)
	}
	return nil
}

// Size returns total bytes stored.
func (a *ArtifactStore) Size() int64 {
	var total int64
	_ = filepath.Walk(a.dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}
