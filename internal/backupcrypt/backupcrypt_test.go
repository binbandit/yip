package backupcrypt

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"testing"
)

func seal(t *testing.T, plain []byte, pass string) []byte {
	t.Helper()
	var out bytes.Buffer
	w, err := NewWriter(&out, []byte(pass))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func open(enc []byte, pass string) ([]byte, error) {
	r, err := NewReader(bytes.NewReader(enc), []byte(pass))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

func TestRoundTripAcrossChunkBoundaries(t *testing.T) {
	for _, size := range []int{0, 1, chunkSize - 1, chunkSize, chunkSize + 1, 3*chunkSize + 17} {
		plain := make([]byte, size)
		_, _ = rand.Read(plain)
		enc := seal(t, plain, "correct horse battery")
		if bytes.Contains(enc, plain[:min(64, len(plain))]) && size >= 64 {
			t.Fatalf("plaintext visible in ciphertext")
		}
		got, err := open(enc, "correct horse battery")
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("size %d: round trip failed: %v", size, err)
		}
	}
}

func TestRejectsWrongPassphraseTamperingAndTruncation(t *testing.T) {
	plain := make([]byte, 2*chunkSize+100)
	_, _ = rand.Read(plain)
	enc := seal(t, plain, "correct horse battery")
	if _, err := open(enc, "incorrect horse battery"); !errors.Is(err, ErrPassphrase) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	tampered := append([]byte{}, enc...)
	tampered[len(tampered)-10] ^= 1
	if _, err := open(tampered, "correct horse battery"); !errors.Is(err, ErrPassphrase) {
		t.Fatalf("tampering: %v", err)
	}
	// Dropping the final chunk (cutting at a chunk boundary) is detected.
	hdrEnd := bytes.IndexByte(enc[len(magic):], '\n') + len(magic) + 1
	firstTwo := hdrEnd + 2*(4+chunkSize+16)
	if _, err := open(enc[:firstTwo], "correct horse battery"); !errors.Is(err, ErrPassphrase) {
		t.Fatalf("truncation: %v", err)
	}
	if _, err := open(append(append([]byte{}, enc...), 0), "correct horse battery"); !errors.Is(err, ErrPassphrase) {
		t.Fatalf("appended data: %v", err)
	}
	if _, err := NewWriter(io.Discard, []byte("short")); err == nil {
		t.Fatalf("a short passphrase should be refused")
	}
	if !IsEncrypted(bytes.NewReader(enc)) || IsEncrypted(bytes.NewReader([]byte("SQLite format 3"))) {
		t.Fatalf("IsEncrypted misdetects")
	}
}
