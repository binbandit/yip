package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestWaitForOwnerRetriesFailedChecks(t *testing.T) {
	answers := []struct {
		need bool
		err  error
	}{{true, nil}, {true, errors.New("database is locked")}, {false, errors.New("database is locked")}, {true, nil}, {false, nil}}
	calls := 0
	check := func(context.Context) (bool, error) {
		a := answers[calls]
		calls++
		return a.need, a.err
	}
	var logs bytes.Buffer
	if !waitForOwner(context.Background(), check, time.Millisecond, slog.New(slog.NewTextHandler(&logs, nil))) {
		t.Fatal("the wait should finish once setup is done")
	}
	if calls != len(answers) {
		t.Fatalf("a failed check must be retried, not taken as finished: stopped after %d checks", calls)
	}
	if n := strings.Count(logs.String(), "database is locked"); n != 1 {
		t.Fatalf("a repeated failure should be logged once, got %d:\n%s", n, logs.String())
	}
}

func TestWaitForOwnerStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	check := func(context.Context) (bool, error) {
		if calls++; calls == 3 {
			cancel()
		}
		return true, nil
	}
	if waitForOwner(ctx, check, time.Millisecond, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))) {
		t.Fatal("a cancelled wait must not report setup as finished")
	}
}
