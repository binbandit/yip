package session

import "time"

// RefreshWindow is how long a refreshed token lives.
const RefreshWindow = time.Hour

// Refresh exchanges a still-valid token for a new one.
func Refresh(t Token, now time.Time) (Token, error) {
	if !validSignature(t) {
		return Token{}, ErrInvalid
	}
	if now.After(t.ExpiresAt) {
		return Token{}, ErrExpired
	}
	return Sign(t.Subject, now.Add(RefreshWindow)), nil
}
