package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/auth"
	"github.com/binbandit/yip/internal/bridge"
	"github.com/binbandit/yip/internal/buildinfo"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/internal/providers/claude"
	"github.com/binbandit/yip/internal/providers/codex"
	"github.com/binbandit/yip/internal/providers/cursor"
	"github.com/binbandit/yip/internal/providers/fake"
	"github.com/binbandit/yip/internal/providers/opencode"
	"github.com/binbandit/yip/internal/providers/pi"
	"github.com/binbandit/yip/internal/providers/worker"
	"github.com/binbandit/yip/internal/runner"
)

const defaultProviders = "codex,claude,cursor,opencode,pi"

func init() {
	// The hub asks the fake provider's director for scripts only for
	// engineers explicitly configured with the fake provider.
	hub.FakeScripter = fake.Direct
}

func defaultRunnerDir() string {
	if d := os.Getenv("YIP_RUNNER_STATE"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".yip", "runner")
}

// adapters builds the provider adapters enabled on this runner.
func adapters(list string) (map[string]providers.Adapter, error) {
	out := map[string]providers.Adapter{}
	for _, name := range strings.Split(list, ",") {
		switch strings.TrimSpace(name) {
		case "":
		case "codex":
			out["codex"] = codex.New()
		case "claude":
			out["claude"] = claude.New()
		case "cursor":
			out["cursor"] = cursor.New()
		case "opencode":
			out["opencode"] = opencode.New()
		case "pi":
			out["pi"] = pi.New()
		case "fake":
			delay := 1200 * time.Millisecond
			if v := os.Getenv("YIP_FAKE_DELAY"); v != "" {
				if d, err := time.ParseDuration(v); err == nil {
					delay = d
				}
			}
			out["fake"] = fake.New(delay)
		default:
			return nil, fmt.Errorf("unknown provider %q (choose from %s, fake)", name, defaultProviders)
		}
	}
	return out, nil
}

func runPair(args []string) error {
	fs := flag.NewFlagSet("runner pair", flag.ExitOnError)
	state := fs.String("state", defaultRunnerDir(), "runner state directory")
	hubURL := fs.String("hub", "", "hub runner URL (https://host:7443)")
	fp := fs.String("fingerprint", "", "hub CA fingerprint shown with the pairing command")
	token := fs.String("token", "", "single-use enrollment token")
	name := fs.String("name", "", "machine name (defaults to the hostname)")
	_ = fs.Parse(args)
	if *hubURL == "" || *fp == "" || *token == "" {
		return errors.New("--hub, --fingerprint, and --token are required (copy the command from Machines → Add machine)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	id, err := runner.Pair(ctx, *state, *hubURL, *fp, *token, *name)
	if err != nil {
		return err
	}
	fmt.Printf("Paired %q as node %s.\nRun it with: yip runner --state %s\n", id.Name, id.NodeID, *state)
	return nil
}

func runRunner(args []string) error {
	fs := flag.NewFlagSet("runner", flag.ExitOnError)
	state := fs.String("state", defaultRunnerDir(), "runner state directory")
	slots := fs.Int("slots", 2, "concurrent runs on this machine")
	provs := fs.String("providers", defaultProviders, "providers this runner may use")
	profile := fs.String("profile", cmp.Or(os.Getenv("YIP_EXECUTION_PROFILE"), "native"), "execution profile: native, container (whole runner), or docker (ephemeral agents)")
	image := fs.String("docker-image", cmp.Or(os.Getenv("YIP_DOCKER_IMAGE"), runner.DefaultDockerImage), "local image for ephemeral agents (never pulled automatically)")
	importEnv := fs.String("docker-env", "", "comma-separated environment variable names to explicitly import for harness authentication and MCP")
	serverName := fs.String("server-name", "", "override the TLS name used to verify the hub")
	_ = fs.Parse(args)
	ad, err := adapters(*provs)
	if err != nil {
		return err
	}
	r, err := runner.New(runner.Options{StateDir: *state, Slots: *slots, Adapters: ad, ExecutionProfile: *profile,
		Docker:  runner.DockerOptions{Image: *image, Env: splitList(*importEnv)},
		Version: buildinfo.Version, Logger: logger(), ServerName: *serverName})
	if err != nil {
		return err
	}
	ctx, cancel := signalContext()
	defer cancel()
	logger().Info("runner starting", "node", r.ID().NodeID, "name", r.ID().Name, "hub", r.ID().HubURL, "providers", *provs)
	return r.Run(ctx)
}

func runBridge(args []string) error {
	fs := flag.NewFlagSet("bridge", flag.ExitOnError)
	mode := fs.String("mode", "conversation", "run mode (edit, readonly, conversation)")
	_ = fs.Parse(args)
	return bridge.RunFromEnv(*mode, buildinfo.Version)
}

// runAgentWorker is the image's internal stdio entrypoint, not a paired runner.
// Its environment was constructed explicitly by the host Docker runtime.
func runAgentWorker(args []string) error {
	fs := flag.NewFlagSet("agent-worker", flag.ContinueOnError)
	provider := fs.String("provider", "", "provider adapter to serve over stdio")
	toolchains := fs.Bool("toolchains", false, "report toolchains inside the image")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := signalContext()
	defer cancel()
	if *toolchains {
		return json.NewEncoder(os.Stdout).Encode(runner.ProbeToolchains(ctx))
	}
	var adapter providers.Adapter
	switch *provider {
	case "codex":
		adapter = codex.New(codex.WithProbeEnv(os.Environ()))
	case "claude":
		adapter = claude.New(claude.Options{Env: os.Environ()})
	case "cursor":
		adapter = cursor.New(cursor.WithProbeEnv(os.Environ()))
	case "fake":
		adapter = fake.New(0)
	default:
		return fmt.Errorf("unknown worker provider %q", *provider)
	}
	return worker.Serve(ctx, os.Stdin, os.Stdout, adapter, buildinfo.Version)
}

// startLocalRunner pairs a runner on the hub's own machine through the same
// enrollment and certificate path as any other machine, then connects to the
// runner listener over mutual TLS.
func startLocalRunner(ctx context.Context, h *hub.Hub, f hubFlags, log *slog.Logger) (func(), error) {
	dir := filepath.Join(f.data, "local-runner")
	if _, err := runner.LoadIdentity(dir); err != nil {
		host, _ := os.Hostname()
		name := cmp.Or(strings.Split(host, ".")[0], "This machine")
		keyPEM, csrPEM, err := auth.NewNodeKeyAndCSR(name)
		if err != nil {
			return nil, err
		}
		pr, err := h.PairLocal(ctx, name, csrPEM)
		if err != nil {
			return nil, fmt.Errorf("pair local runner: %w", err)
		}
		_, port, _ := net.SplitHostPort(f.runnerListen)
		if _, err := runner.SaveLocalIdentity(dir, runner.Identity{NodeID: pr.NodeID, Name: name, HubURL: "https://127.0.0.1:" + port,
			Fingerprint: h.CA().Fingerprint(), PairedAt: time.Now().UTC().Format(time.RFC3339)}, keyPEM, []byte(pr.CertPEM), []byte(pr.CAPEM)); err != nil {
			return nil, err
		}
	}
	ad, err := adapters(f.localProviders)
	if err != nil {
		return nil, err
	}
	r, err := runner.New(runner.Options{StateDir: dir, Slots: f.localSlots, Adapters: ad, Version: buildinfo.Version,
		ExecutionProfile: f.localProfile, Docker: runner.DockerOptions{Image: f.localDockerImage, Env: splitList(f.localDockerEnv)},
		Logger: log.With("component", "local-runner"), ServerName: "127.0.0.1"})
	if err != nil {
		return nil, err
	}
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := r.Run(rctx); err != nil {
			log.Error("local runner stopped", "err", err)
		}
	}()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(40 * time.Second):
		}
	}, nil
}
