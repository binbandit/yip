package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
)

const idemHeader = "Idempotency-Key"

// idempotent runs a state-changing JSON request at most once per
// Idempotency-Key: a repeat of the same request replays the first response
// (marked Idempotent-Replayed), a different request under the same key is
// refused, and one still in progress asks the client to retry shortly.
// Server errors release the key so a retry runs anew. Uploads (non-JSON
// bodies) aren't covered.
func (s *Server) idempotent(w http.ResponseWriter, r *http.Request, userID string, next func(http.ResponseWriter, *http.Request)) {
	key := strings.TrimSpace(r.Header.Get(idemHeader))
	if key == "" || !isUnsafe(r.Method) || strings.HasPrefix(r.Header.Get("Content-Type"), "application/octet-stream") {
		next(w, r)
		return
	}
	if len(key) < 8 || len(key) > 128 {
		s.fail(w, r, domain.Invalid("Idempotency-Key must be 8–128 characters."))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxJSONBody))
	if err != nil {
		s.fail(w, r, domain.Invalid("The request body is too large."))
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	sum := sha256.Sum256(append([]byte(r.Method+" "+r.URL.RequestURI()+"\n"), body...))
	hash := hex.EncodeToString(sum[:])

	var prior *store.IdemRecord
	if err := s.hub.Store().Tx(r.Context(), func(tx *sql.Tx) error {
		var err error
		prior, err = store.BeginIdempotent(r.Context(), tx, userID, key, r.Method, r.URL.Path, hash, time.Now())
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if prior != nil {
		switch {
		case prior.RequestHash != hash:
			s.fail(w, r, domain.Invalid("This Idempotency-Key was already used for a different request."))
		case prior.State != "done":
			w.Header().Set("Retry-After", "1")
			s.fail(w, r, domain.Conflict("The same request is still being processed; try again in a moment."))
		default:
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Idempotent-Replayed", "true")
			w.WriteHeader(prior.Status)
			_, _ = w.Write(prior.Body)
		}
		return
	}
	rec := &recorder{ResponseWriter: w, status: http.StatusOK}
	next(rec, r)
	// Record with a fresh context: the request's may already be done.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.hub.Store().Tx(ctx, func(tx *sql.Tx) error {
		if rec.status >= 500 || rec.overflow {
			return store.ReleaseIdempotent(ctx, tx, userID, key)
		}
		return store.FinishIdempotent(ctx, tx, userID, key, rec.status, rec.buf.Bytes())
	})
}

// recorder passes a response through while keeping a copy of it (up to
// 1 MiB) for replay.
type recorder struct {
	http.ResponseWriter
	status   int
	buf      bytes.Buffer
	overflow bool
}

func (r *recorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	if !r.overflow {
		if r.buf.Len()+len(b) > maxJSONBody {
			r.overflow = true
			r.buf.Reset()
		} else {
			r.buf.Write(b)
		}
	}
	return r.ResponseWriter.Write(b)
}
