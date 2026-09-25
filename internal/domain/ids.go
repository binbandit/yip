package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

// NewID returns a UUIDv7 string. Time-ordered IDs keep indexes compact; the
// hub-issued event sequence, not IDs or client clocks, determines ordering.
func NewID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}

var idPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ValidID reports whether s is a well-formed yip identifier. Tool calls with
// malformed IDs are rejected before any lookup.
func ValidID(s string) bool { return idPattern.MatchString(s) }

// RandomToken returns n random bytes, URL-safe base64 encoded.
func RandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashToken returns the hex SHA-256 of a bearer token; only hashes are stored.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Now returns the current UTC time truncated to milliseconds, the precision
// persisted in the database.
func Now() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }

// Handle derives a mention handle from a display name ("Mira Chen" → "mira-chen").
func Handle(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case !lastDash && b.Len() > 0:
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// Short returns a short prefix of an ID for branch names and display.
func Short(id string) string {
	s := strings.ReplaceAll(id, "-", "")
	if len(s) > 12 {
		return s[len(s)-12:]
	}
	return s
}
