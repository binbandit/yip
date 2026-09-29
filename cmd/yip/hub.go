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
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/forge"
	"github.com/binbandit/yip/internal/forge/github"
	"github.com/binbandit/yip/internal/httpapi"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/internal/runner"
	"github.com/binbandit/yip/protocol"
	"github.com/binbandit/yip/web"
)

type hubFlags struct {
	data             string
	listen           string
	runnerListen     string
	runnerURL        string
	runnerHosts      string
	tlsCert          string
	insecureHTTP     bool
	tlsKey           string
	secureCookies    bool
	allowedOrigins   string
	localRunner      bool
	localSlots       int
	localProviders   string
	localProfile     string
	localDockerImage string
	localDockerEnv   string
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
	fs.BoolVar(&f.insecureHTTP, "insecure-http", false, "allow plain HTTP on a non-loopback --listen address (a trusted local network only: the password and session cross it unencrypted)")
	fs.StringVar(&f.allowedOrigins, "allowed-origin", "", "extra comma-separated origins allowed for state-changing requests")
	fs.BoolVar(&f.localRunner, "local-runner", false, "also run a runner on this machine (paired through the normal enrollment path)")
	fs.IntVar(&f.localSlots, "local-slots", 2, "concurrent runs for the local runner")
	fs.StringVar(&f.localProviders, "local-providers", defaultProviders, "providers the local runner may use")
	fs.StringVar(&f.localProfile, "local-profile", "native", "local runner execution: native, container, or docker (ephemeral agents)")
	fs.StringVar(&f.localDockerImage, "local-docker-image", runner.DefaultDockerImage, "local image for ephemeral agents")
	fs.StringVar(&f.localDockerEnv, "local-docker-env", "", "comma-separated environment variable names explicitly imported into agent containers")
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

// githubRepo asks GitHub about a repository being added: through the GitHub
// CLI signed in on this machine first, so a private repository its account can
// see needs no stored token, then the REST API with the stored forge
// credential (or anonymously, which sees public repositories).
func githubRepo(ctx context.Context, h *hub.Hub, owner, name string) (github.RepoInfo, error) {
	info, cliErr := github.ViewWithCLI(ctx, owner, name)
	if cliErr == nil {
		return info, nil
	}
	host := "github.com"
	token := func(ctx context.Context) (string, error) {
		t, err := h.ForgeToken(ctx, "github", host)
		if err != nil {
			return "", nil // no stored credential: ask anonymously
		}
		return t, nil
	}
	info, apiErr := github.New(github.Options{Host: host, Token: token}).Repo(ctx, forge.RepoRef{Host: host, Owner: owner, Name: name})
	if apiErr == nil {
		return info, nil
	}
	if !errors.Is(cliErr, github.ErrCLIUnavailable) {
		return info, cliErr // gh was asked and answered; its answer is the owner's view
	}
	return info, apiErr
}

// startHub opens the hub and its listeners. It returns a stop function.
func startHub(ctx context.Context, f hubFlags, log *slog.Logger) (*hub.Hub, func(), error) {
	// Remote access is HTTPS through an explicit setup step (brief §7); plain
	// HTTP beyond this machine needs an explicit, loudly logged opt-in.
	if f.tlsCert == "" && !isLoopbackAddr(f.listen) {
		if !f.insecureHTTP {
			return nil, nil, fmt.Errorf("refusing to serve plain HTTP on %s: the password and session cookie would cross the network unencrypted. Use --tls-cert/--tls-key or Tailscale Serve (docs/operations.md), or pass --insecure-http for a trusted local network", f.listen)
		}
		log.Warn("serving plain HTTP beyond this machine (--insecure-http): use only on a trusted local network", "listen", f.listen)
	}
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
	h, err := hub.Open(ctx, hub.Config{DataDir: f.data, Version: buildinfo.Version, Logger: log, RunnerURL: f.runnerURL,
		ForgeFactory: githubFactory, GitHubRepo: githubRepo, WebhookVerifier: github.New(github.Options{}).VerifyWebhook})
	if err != nil {
		return nil, nil, err
	}
	h.Start(ctx)

	opts := httpapi.Options{SecureCookies: f.secureCookies || f.tlsCert != "", Web: httpapi.WebFS(web.Dist, "dist"), Logger: log,
		AllowedOrigins: splitList(f.allowedOrigins)}
	workspaces, err := httpapi.OpenWorkspaces(ctx, h, opts)
	if err != nil {
		h.Close()
		return nil, nil, err
	}
	browser := &http.Server{Addr: f.listen, Handler: workspaces, ReadHeaderTimeout: 10 * time.Second}
	hosts := append([]string{hostname}, splitList(f.runnerHosts)...)
	if u := strings.TrimPrefix(f.runnerURL, "https://"); u != "" {
		if host, _, err := net.SplitHostPort(u); err == nil {
			hosts = append(hosts, host)
		}
	}
	tlsConf, err := h.CA().ServerTLS(hosts)
	if err != nil {
		workspaces.Close()
		return nil, nil, err
	}
	rln, err := tls.Listen("tcp", f.runnerListen, tlsConf)
	if err != nil {
		workspaces.Close()
		return nil, nil, fmt.Errorf("runner listener: %w", err)
	}
	runnerSrv := &http.Server{Handler: workspaces.RunnerHandler(), ReadHeaderTimeout: 10 * time.Second}
	bln, err := net.Listen("tcp", f.listen)
	if err != nil {
		rln.Close()
		workspaces.Close()
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
		_ = workspaces.Close()
	}
	return h, stop, nil
}

// splitList splits a comma-separated flag value, dropping blank entries.
func splitList(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// openHub opens the hub in a data directory for a one-off operator command.
func openHub(ctx context.Context, data string) (*hub.Hub, error) {
	return hub.Open(ctx, hub.Config{DataDir: data, Version: buildinfo.Version, Logger: logger()})
}

// isLoopbackAddr reports a listen address reachable only from this machine.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
	h, stop, err := startHub(ctx, f, log)
	if err != nil {
		return err
	}
	defer stop()
	need, err := h.NeedsSetup(ctx)
	if err != nil {
		return err
	}
	if need {
		secret, exp, err := h.IssueBootstrapSecret(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("\nNo owner exists yet. Finish setup in your browser:\n\n  http://%s/setup\n\nOne-time setup code (expires %s): %s\n\n", displayAddr(f.listen), exp.Local().Format("15:04"), secret)
	}
	if f.localRunner {
		if !waitForOwner(ctx, h.NeedsSetup, time.Second, log) {
			return nil
		}
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

// waitForOwner blocks until owner setup has created the organisation that
// pairing needs, checking every interval. A failed check is retried, never
// taken as finished. It returns false if ctx ends first.
func waitForOwner(ctx context.Context, needsSetup func(context.Context) (bool, error), every time.Duration, log *slog.Logger) bool {
	lastErr := ""
	for {
		need, err := needsSetup(ctx)
		switch {
		case err != nil && err.Error() != lastErr:
			lastErr = err.Error()
			log.Warn("could not check whether owner setup is finished; retrying", "err", err)
		case err == nil && !need:
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(every):
		}
	}
}

func runSetupCode(args []string) error {
	fs := flag.NewFlagSet("setup-code", flag.ExitOnError)
	data := fs.String("data", defaultDataDir(), "hub data directory")
	_ = fs.Parse(args)
	ctx := context.Background()
	h, err := openHub(ctx, *data)
	if err != nil {
		return err
	}
	defer h.Close()
	need, err := h.NeedsSetup(ctx)
	if err != nil {
		return err
	}
	if !need {
		return errors.New("this hub already has an owner; use `yip owner reset-password` to recover access")
	}
	secret, exp, err := h.IssueBootstrapSecret(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("One-time setup code (expires %s): %s\n", exp.Local().Format("15:04"), secret)
	return nil
}
