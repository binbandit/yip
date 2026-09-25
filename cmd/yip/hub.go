package main

import (
	"context"
	"crypto/tls"
	"errors"
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

	"github.com/binbandit/yip/internal/buildinfo"
	"github.com/binbandit/yip/internal/demo"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/forge"
	"github.com/binbandit/yip/internal/forge/github"
	"github.com/binbandit/yip/internal/httpapi"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/protocol"
	"github.com/binbandit/yip/web"
)

type hubFlags struct {
	data           string
	listen         string
	runnerListen   string
	runnerURL      string
	runnerHosts    string
	tlsCert        string
	tlsKey         string
	secureCookies  bool
	allowedOrigins string
	localRunner    bool
	localSlots     int
	localProviders string
}

func defaultDataDir() string {
	if d := os.Getenv("YIP_DATA"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".yip", "hub")
}

func (f *hubFlags) register(fs *flag.FlagSet, dataDefault string) {
	fs.StringVar(&f.data, "data", dataDefault, "hub data directory (local disk only)")
	fs.StringVar(&f.listen, "listen", "127.0.0.1:7420", "browser API address (loopback by default)")
	fs.StringVar(&f.runnerListen, "runner-listen", ":7443", "runner listener address (mutual TLS)")
	fs.StringVar(&f.runnerURL, "runner-url", "", "URL runners use to reach the runner listener (default https://<hostname>:<port>)")
	fs.StringVar(&f.runnerHosts, "runner-hosts", "", "extra comma-separated hostnames/IPs for the runner listener certificate")
	fs.StringVar(&f.tlsCert, "tls-cert", "", "TLS certificate for the browser listener (enables HTTPS)")
	fs.StringVar(&f.tlsKey, "tls-key", "", "TLS key for the browser listener")
	fs.BoolVar(&f.secureCookies, "secure-cookies", false, "mark session cookies Secure (set when served over HTTPS, e.g. behind Tailscale Serve)")
	fs.StringVar(&f.allowedOrigins, "allowed-origin", "", "extra comma-separated origins allowed for state-changing requests")
	fs.BoolVar(&f.localRunner, "local-runner", false, "also run a runner on this machine (paired through the normal enrollment path)")
	fs.IntVar(&f.localSlots, "local-slots", 2, "concurrent runs for the local runner")
	fs.StringVar(&f.localProviders, "local-providers", "codex,claude,cursor", "providers the local runner may use")
}

func logger() *slog.Logger {
	level := slog.LevelInfo
	if os.Getenv("YIP_DEBUG") != "" {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func githubFactory(ctx context.Context, h *hub.Hub, repo protocol.Repo) (forge.Connector, forge.RepoRef, error) {
	parts := strings.SplitN(repo.ForgeRepo, "/", 2)
	if len(parts) != 2 {
		return nil, forge.RepoRef{}, domain.Invalid("Repository %s has no owner/name forge identity.", repo.Name)
	}
	host := "github.com"
	c := github.New(github.Options{Host: host, Token: func(ctx context.Context) (string, error) { return h.ForgeToken(ctx, "github", host) }})
	return c, forge.RepoRef{Host: host, Owner: parts[0], Name: parts[1]}, nil
}

// startHub opens the hub and its listeners. It returns a stop function.
func startHub(ctx context.Context, f hubFlags, isDemo bool, log *slog.Logger) (*hub.Hub, func(), error) {
	_, rport, err := net.SplitHostPort(f.runnerListen)
	if err != nil {
		return nil, nil, fmt.Errorf("--runner-listen: %w", err)
	}
	hostname, _ := os.Hostname()
	if f.runnerURL == "" {
		host := hostname
		if f.localRunner && hostname == "" {
			host = "127.0.0.1"
		}
		f.runnerURL = "https://" + net.JoinHostPort(host, rport)
	}
	h, err := hub.Open(ctx, hub.Config{DataDir: f.data, Version: buildinfo.Version, Logger: log, RunnerURL: f.runnerURL, Demo: isDemo,
		ForgeFactory: githubFactory})
	if err != nil {
		return nil, nil, err
	}
	h.Start(ctx)

	opts := httpapi.Options{SecureCookies: f.secureCookies || f.tlsCert != "", Web: httpapi.WebFS(web.Dist, "dist"), Logger: log}
	for _, o := range strings.Split(f.allowedOrigins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			opts.AllowedOrigins = append(opts.AllowedOrigins, o)
		}
	}
	browser := &http.Server{Addr: f.listen, Handler: httpapi.New(h, opts), ReadHeaderTimeout: 10 * time.Second}
	hosts := []string{hostname}
	for _, x := range strings.Split(f.runnerHosts, ",") {
		if x = strings.TrimSpace(x); x != "" {
			hosts = append(hosts, x)
		}
	}
	if u := strings.TrimPrefix(f.runnerURL, "https://"); u != "" {
		if host, _, err := net.SplitHostPort(u); err == nil {
			hosts = append(hosts, host)
		}
	}
	tlsConf, err := h.CA().ServerTLS(hosts)
	if err != nil {
		h.Close()
		return nil, nil, err
	}
	rln, err := tls.Listen("tcp", f.runnerListen, tlsConf)
	if err != nil {
		h.Close()
		return nil, nil, fmt.Errorf("runner listener: %w", err)
	}
	runnerSrv := &http.Server{Handler: httpapi.NewRunnerServer(h, func(msg string, args ...any) { log.Info(msg, args...) }), ReadHeaderTimeout: 10 * time.Second}
	bln, err := net.Listen("tcp", f.listen)
	if err != nil {
		rln.Close()
		h.Close()
		return nil, nil, fmt.Errorf("browser listener: %w", err)
	}
	go func() {
		var err error
		if f.tlsCert != "" {
			err = browser.ServeTLS(bln, f.tlsCert, f.tlsKey)
		} else {
			err = browser.Serve(bln)
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("browser listener stopped", "err", err)
		}
	}()
	go func() {
		if err := runnerSrv.Serve(rln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("runner listener stopped", "err", err)
		}
	}()
	scheme := "http"
	if f.tlsCert != "" {
		scheme = "https"
	}
	log.Info("hub listening", "web", scheme+"://"+displayAddr(f.listen), "runners", f.runnerURL, "fingerprint", h.CA().Fingerprint(), "data", f.data)
	stop := func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = browser.Shutdown(sctx)
		_ = runnerSrv.Shutdown(sctx)
		_ = h.Close()
	}
	return h, stop, nil
}

func displayAddr(a string) string {
	if strings.HasPrefix(a, ":") {
		return "localhost" + a
	}
	return a
}

func runHub(args []string) error {
	fs := flag.NewFlagSet("hub", flag.ExitOnError)
	var f hubFlags
	f.register(fs, defaultDataDir())
	_ = fs.Parse(args)
	log := logger()
	ctx, cancel := signalContext()
	defer cancel()
	h, stop, err := startHub(ctx, f, false, log)
	if err != nil {
		return err
	}
	defer stop()
	if need, _ := h.NeedsSetup(ctx); need {
		secret, exp, err := h.IssueBootstrapSecret(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("\nNo owner exists yet. Finish setup in your browser:\n\n  http://%s/setup\n\nOne-time setup code (expires %s): %s\n\n", displayAddr(f.listen), exp.Local().Format("15:04"), secret)
	}
	if f.localRunner {
		stopRunner, err := startLocalRunner(ctx, h, f, log)
		if err != nil {
			return err
		}
		defer stopRunner()
	}
	<-ctx.Done()
	log.Info("shutting down")
	return nil
}

func runSetupCode(args []string) error {
	fs := flag.NewFlagSet("setup-code", flag.ExitOnError)
	data := fs.String("data", defaultDataDir(), "hub data directory")
	_ = fs.Parse(args)
	ctx := context.Background()
	h, err := hub.Open(ctx, hub.Config{DataDir: *data, Version: buildinfo.Version, Logger: logger()})
	if err != nil {
		return err
	}
	defer h.Close()
	if need, _ := h.NeedsSetup(ctx); !need {
		return errors.New("this hub already has an owner; use `yip owner reset-password` to recover access")
	}
	secret, exp, err := h.IssueBootstrapSecret(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("One-time setup code (expires %s): %s\n", exp.Local().Format("15:04"), secret)
	return nil
}

func runDemo(args []string) error {
	fs := flag.NewFlagSet("demo", flag.ExitOnError)
	var f hubFlags
	home, _ := os.UserHomeDir()
	f.register(fs, filepath.Join(home, ".yip", "demo"))
	reset := fs.Bool("reset", false, "delete the demo data directory first")
	_ = fs.Parse(args)
	if *reset {
		if !strings.Contains(f.data, "demo") {
			return fmt.Errorf("refusing to delete %s: --reset only removes directories named for the demo", f.data)
		}
		if err := os.RemoveAll(f.data); err != nil {
			return err
		}
	}
	f.localRunner = true
	f.localProviders = "fake"
	log := logger()
	ctx, cancel := signalContext()
	defer cancel()
	h, stop, err := startHub(ctx, f, true, log)
	if err != nil {
		return err
	}
	defer stop()
	credFile := filepath.Join(f.data, "demo-credentials.txt")
	if need, _ := h.NeedsSetup(ctx); need {
		repos, err := demo.MaterializeRepos(filepath.Join(f.data, "fixtures"))
		if err != nil {
			return fmt.Errorf("fixture repositories: %w", err)
		}
		password := "demo-" + domain.RandomToken(9)
		if _, err := demo.Seed(ctx, h, repos, "Brayden", "brayden", password); err != nil {
			return fmt.Errorf("seed demo: %w", err)
		}
		_ = os.WriteFile(credFile, []byte("handle: brayden\npassword: "+password+"\n"), 0o600)
	}
	creds, _ := os.ReadFile(credFile)
	stopRunner, err := startLocalRunner(ctx, h, f, log)
	if err != nil {
		return err
	}
	defer stopRunner()
	fmt.Printf("\nyip demo workspace (engineers use the deterministic fake provider — no models are called)\n\n  Open http://%s and sign in with:\n  %s\n  Data: %s\n\n",
		displayAddr(f.listen), strings.ReplaceAll(strings.TrimSpace(string(creds)), "\n", "\n  "), f.data)
	<-ctx.Done()
	return nil
}
