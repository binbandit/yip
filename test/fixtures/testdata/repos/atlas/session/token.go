// Package session validates and refreshes Atlas session tokens.
package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

var (
	ErrInvalid = errors.New("session: invalid signature")
	ErrExpired = errors.New("session: expired")
)

// Token is a signed session token.
type Token struct {
	Subject   string
	ExpiresAt time.Time
	Signature string
}

var key = []byte("atlas-fixture-signing-key")

// Sign returns a signed token for subject that expires at exp.
func Sign(subject string, exp time.Time) Token {
	t := Token{Subject: subject, ExpiresAt: exp}
	t.Signature = signature(t)
	return t
}

func signature(t Token) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(t.Subject))
	m.Write([]byte(t.ExpiresAt.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(m.Sum(nil))
}

func validSignature(t Token) bool {
	return hmac.Equal([]byte(t.Signature), []byte(signature(t)))
}
