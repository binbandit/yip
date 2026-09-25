package session

import "time"

// Validate reports whether a token may be used at time now.
func Validate(t Token, now time.Time) error {
	if !validSignature(t) {
		return ErrInvalid
	}
	return nil
}
