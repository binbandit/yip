// browserharness is a test-only executable. It runs scripted scenarios through
// the real hub, native runner, workspace isolation and MCP bridge. It is never
// linked into the shipped yip binary and never starts an installed AI provider.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/binbandit/yip/internal/auth"
	manifest "github.com/binbandit/yip/internal/context"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/httpapi"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/internal/runner"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	data := flag.String("data", "", "temporary test data directory")
	listen := flag.String("listen", "127.0.0.1:0", "loopback browser listener")
	runnerListen := flag.String("runner-listen", "127.0.0.1:0", "loopback runner listener")
	bridge := flag.String("bridge", "", "built yip executable for MCP tools")
	delay := flag.Duration("delay", 250*time.Millisecond, "scripted pause duration")
	flag.Parse()
	if *data == "" || *bridge == "" {
		return fmt.Errorf("--data and --bridge are required")
	}
	for _, addr := range []string{*listen, *runnerListen} {
		host, _, err := net.SplitHostPort(addr)
		if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return fmt.Errorf("test listeners must use loopback: %s", addr)
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	h, err := hub.Open(ctx, hub.Config{DataDir: *data, Version: "browser-test", Logger: log, RunnerURL: "https://" + *runnerListen})
	if err != nil {
		return err
	}
	defer h.Close()
	repos, err := MaterializeRepos(filepath.Join(*data, "fixtures"))
	if err != nil {
		return err
	}
	password := "test-" + domain.RandomToken(9)
	if _, err := Seed(ctx, h, repos, "Brayden", "brayden", password); err != nil {
		return err
	}
	h.Start(ctx)
	workspaces, err := httpapi.OpenWorkspaces(ctx, h, httpapi.Options{Web: httpapi.WebFS(web.Dist, "dist"), Logger: log})
	if err != nil {
		return err
	}
	defer workspaces.Close()
	tlsConfig, err := h.CA().ServerTLS([]string{"127.0.0.1"})
	if err != nil {
		return err
	}
	rl, err := tls.Listen("tcp", *runnerListen, tlsConfig)
	if err != nil {
		return err
	}
	defer rl.Close()
	bl, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	defer bl.Close()
	browser := &http.Server{Handler: workspaces, ReadHeaderTimeout: 10 * time.Second}
	nodes := &http.Server{Handler: workspaces.RunnerHandler(), ReadHeaderTimeout: 10 * time.Second}
	defer browser.Close()
	defer nodes.Close()
	go browser.Serve(bl)
	go nodes.Serve(rl)
	key, csr, err := auth.NewNodeKeyAndCSR("Browser fixture")
	if err != nil {
		return err
	}
	pair, err := h.PairLocal(ctx, "Browser fixture", csr)
	if err != nil {
		return err
	}
	state := filepath.Join(*data, "runner")
	_, err = runner.SaveLocalIdentity(state, runner.Identity{NodeID: pair.NodeID, Name: "Browser fixture", HubURL: "https://" + rl.Addr().String(), Fingerprint: h.CA().Fingerprint(), PairedAt: time.Now().UTC().Format(time.RFC3339)}, key, []byte(pair.CertPEM), []byte(pair.CAPEM))
	if err != nil {
		return err
	}
	adapter := New(*delay)
	adapter.Script = func(ctx context.Context, id string) (json.RawMessage, error) {
		if draft, err := store.GetEngineerDraft(ctx, h.Store().R(), id); err == nil {
			output := `{"role":"Accessibility engineer","description":"Builds inclusive interfaces.","capabilityTags":["accessibility","ui"],"instructions":"Test keyboard navigation and report browser evidence."}`
			if strings.Contains(draft.Request.Description, "[invalid draft]") {
				output = `{"role":"Incomplete"}`
			}
			wait := "250ms"
			if strings.Contains(draft.Request.Description, "[slow draft]") {
				wait = "3s"
			}
			return json.Marshal(Script{Steps: []Step{{Sleep: wait}, {Final: output}}})
		}
		if draft, err := store.GetEngineerDraft(ctx, h.Store().R(), id); err == nil {
			text := `{"role":"Accessibility engineer","description":"Builds inclusive interfaces and checks keyboard navigation.","capabilityTags":["accessibility","frontend"],"instructions":"Test keyboard and screen-reader behavior. Preserve established interface contracts."}`
			if strings.Contains(draft.Request.Description, "malformed fixture") {
				text = `{"role":"Incomplete"}`
			}
			delay := "800ms"
			if strings.Contains(draft.Request.Description, "slow fixture") {
				delay = "3s"
			}
			return json.Marshal(Script{Steps: []Step{{Sleep: delay}, {Final: text}}})
		}
		row, err := store.GetRun(ctx, h.Store().R(), id)
		if err != nil {
			return nil, err
		}
		var m manifest.Manifest
		if err := json.Unmarshal(row.Manifest, &m); err != nil {
			return nil, err
		}
		return Direct(&m), nil
	}
	r, err := runner.New(runner.Options{StateDir: state, Slots: 2, Adapters: map[string]providers.Adapter{"codex": adapter}, BridgeExe: *bridge, Version: "browser-test", Logger: log, ServerName: "127.0.0.1"})
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	fmt.Printf("Scripted browser test fixture; no model is called.\nhandle: brayden\npassword: %s\n", password)
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		return nil
	}
}
