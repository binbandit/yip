// Package backupcrypt encrypts a backup archive with a passphrase.
//
// Format ("yip encrypted backup v1"): the line "YIPBAK1\n", a JSON header
// line with the Argon2id parameters and salt, then a sequence of chunks.
// Each chunk is a 4-byte big-endian length followed by AES-256-GCM
// ciphertext of up to 64 KiB of plaintext. The nonce is a random 8-byte
// prefix plus the chunk counter, and the additional data marks the final
// chunk, so reordering, truncation, or appending is detected.
package backupcrypt

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

const (
	magic     = "YIPBAK1\n"
	chunkSize = 64 << 10
	// MinPassphrase is the shortest passphrase accepted for a new backup.
	MinPassphrase = 12
)

// ErrPassphrase means the passphrase is wrong or the file was altered.
var ErrPassphrase = errors.New("the passphrase is wrong, or the backup file was altered or cut short")

type header struct {
	KDF     string `json:"kdf"`
	Time    uint32 `json:"time"`
	Memory  uint32 `json:"memoryKiB"`
	Threads uint8  `json:"threads"`
	Salt    []byte `json:"salt"`
	Nonce   []byte `json:"noncePrefix"`
}

// IsEncrypted reports whether r starts like an encrypted backup.
func IsEncrypted(r io.Reader) bool {
	b := make([]byte, len(magic))
	_, err := io.ReadFull(r, b)
	return err == nil && string(b) == magic
}

func key(pass []byte, h header) []byte {
	return argon2.IDKey(pass, h.Salt, h.Time, h.Memory, h.Threads, 32)
}

// Writer encrypts everything written to it; Close writes the final chunk.
type Writer struct {
	w       io.Writer
	aead    cipher.AEAD
	prefix  []byte
	counter uint32
	buf     []byte
	closed  bool
}

// NewWriter writes the header and returns a Writer for the plaintext.
func NewWriter(w io.Writer, passphrase []byte) (*Writer, error) {
	if len(passphrase) < MinPassphrase {
		return nil, fmt.Errorf("use a passphrase of at least %d characters", MinPassphrase)
	}
	h := header{KDF: "argon2id", Time: 3, Memory: 64 << 10, Threads: 4, Salt: make([]byte, 16), Nonce: make([]byte, 8)}
	if _, err := rand.Read(h.Salt); err != nil {
		return nil, err
	}
	if _, err := rand.Read(h.Nonce); err != nil {
		return nil, err
	}
	aead, err := newAEAD(key(passphrase, h))
	if err != nil {
		return nil, err
	}
	hb, _ := json.Marshal(h)
	if _, err := io.WriteString(w, magic); err != nil {
		return nil, err
	}
	if _, err := w.Write(append(hb, '\n')); err != nil {
		return nil, err
	}
	return &Writer{w: w, aead: aead, prefix: h.Nonce, buf: make([]byte, 0, chunkSize)}, nil
}

func newAEAD(k []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func nonce(prefix []byte, n uint32) []byte {
	out := make([]byte, 12)
	copy(out, prefix)
	binary.BigEndian.PutUint32(out[8:], n)
	return out
}

func (w *Writer) Write(p []byte) (int, error) {
	if w.closed {
		return 0, errors.New("write after close")
	}
	n := len(p)
	for len(p) > 0 {
		take := min(chunkSize-len(w.buf), len(p))
		w.buf = append(w.buf, p[:take]...)
		p = p[take:]
		if len(w.buf) == chunkSize {
			if err := w.flush(false); err != nil {
				return 0, err
			}
		}
	}
	return n, nil
}

func (w *Writer) flush(final bool) error {
	if w.counter == ^uint32(0) {
		return errors.New("backup too large")
	}
	ad := []byte{0}
	if final {
		ad[0] = 1
	}
	ct := w.aead.Seal(nil, nonce(w.prefix, w.counter), w.buf, ad)
	w.counter++
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(ct)))
	if _, err := w.w.Write(l[:]); err != nil {
		return err
	}
	if _, err := w.w.Write(ct); err != nil {
		return err
	}
	w.buf = w.buf[:0]
	return nil
}

// Close writes the final chunk (possibly empty).
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	return w.flush(true)
}

// Reader decrypts an encrypted backup. Reads fail with ErrPassphrase on a
// wrong passphrase or any alteration, including a missing final chunk.
type Reader struct {
	r       *bufio.Reader
	aead    cipher.AEAD
	prefix  []byte
	counter uint32
	buf     []byte
	done    bool
}

// NewReader reads the header and derives the key.
func NewReader(r io.Reader, passphrase []byte) (*Reader, error) {
	br := bufio.NewReader(r)
	m := make([]byte, len(magic))
	if _, err := io.ReadFull(br, m); err != nil || string(m) != magic {
		return nil, errors.New("not an encrypted yip backup")
	}
	line, err := br.ReadBytes('\n')
	if err != nil {
		return nil, errors.New("the backup header is truncated")
	}
	var h header
	if err := json.Unmarshal(line, &h); err != nil || h.KDF != "argon2id" || len(h.Salt) < 16 || len(h.Nonce) != 8 ||
		h.Memory > 1<<21 || h.Time > 64 || h.Threads == 0 {
		return nil, errors.New("the backup header is not recognised")
	}
	aead, err := newAEAD(key(passphrase, h))
	if err != nil {
		return nil, err
	}
	return &Reader{r: br, aead: aead, prefix: h.Nonce}, nil
}

func (r *Reader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 {
		if r.done {
			return 0, io.EOF
		}
		var l [4]byte
		if _, err := io.ReadFull(r.r, l[:]); err != nil {
			return 0, ErrPassphrase // cut short before the final chunk
		}
		n := binary.BigEndian.Uint32(l[:])
		if n < uint32(r.aead.Overhead()) || n > chunkSize+uint32(r.aead.Overhead()) {
			return 0, ErrPassphrase
		}
		ct := make([]byte, n)
		if _, err := io.ReadFull(r.r, ct); err != nil {
			return 0, ErrPassphrase
		}
		pt, err := r.aead.Open(nil, nonce(r.prefix, r.counter), ct, []byte{0})
		if err != nil {
			if pt, err = r.aead.Open(nil, nonce(r.prefix, r.counter), ct, []byte{1}); err != nil {
				return 0, ErrPassphrase
			}
			r.done = true
			if _, err := r.r.ReadByte(); err != io.EOF {
				return 0, ErrPassphrase // data after the final chunk
			}
		}
		r.counter++
		r.buf = pt
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}
