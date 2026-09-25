package session

import (
	"testing"
	"time"
)

var base = time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)

func TestValidateAcceptsLiveToken(t *testing.T) {
	tok := Sign("mira", base.Add(time.Minute))
	if err := Validate(tok, base); err != nil {
		t.Fatalf("live token rejected: %v", err)
	}
}

func TestValidateRejectsBadSignature(t *testing.T) {
	tok := Sign("mira", base.Add(time.Minute))
	tok.Subject = "mallory"
	if err := Validate(tok, base); err != ErrInvalid {
		t.Fatalf("got %v, want ErrInvalid", err)
	}
}

// Seeded regression: Atlas currently accepts expired sessions.
func TestValidateRejectsExpiredToken(t *testing.T) {
	tok := Sign("mira", base.Add(-time.Second))
	if err := Validate(tok, base); err != ErrExpired {
		t.Fatalf("expired token: got %v, want ErrExpired", err)
	}
}

func TestRefreshIssuesNewToken(t *testing.T) {
	tok := Sign("mira", base.Add(time.Minute))
	next, err := Refresh(tok, base)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !next.ExpiresAt.Equal(base.Add(RefreshWindow)) {
		t.Fatalf("refreshed expiry %v", next.ExpiresAt)
	}
}
