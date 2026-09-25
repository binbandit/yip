package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/binbandit/yip/internal/hub"
)

// Temporary: filled in as the runner and operator tools land.

func startLocalRunner(ctx context.Context, h *hub.Hub, f hubFlags, log *slog.Logger) (func(), error) {
	log.Warn("local runner not yet available in this build")
	return func() {}, nil
}

var errPending = errors.New("not yet implemented in this build")

func runPair(args []string) error    { return errPending }
func runRunner(args []string) error  { return errPending }
func runBridge(args []string) error  { return errPending }
func runDoctor(args []string) error  { return errPending }
func runBackup(args []string) error  { return errPending }
func runRestore(args []string) error { return errPending }
func runOwner(args []string) error   { return errPending }
func runForge(args []string) error   { return errPending }
func runService(args []string) error { return errPending }
