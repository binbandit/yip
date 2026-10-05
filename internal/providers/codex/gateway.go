package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

// Decode only structural provider fields. Credentials, headers and endpoint
// values never become installation metadata or diagnostics.
type modelProviderConfig struct {
	EnvKey         string            `json:"env_key"`
	EnvHTTPHeaders map[string]string `json:"env_http_headers"`
	BaseURL        string            `json:"base_url"`
}

func selectedProvider(cfg configReadResponse) (modelProviderConfig, bool) {
	var config struct {
		Provider  string                         `json:"model_provider"`
		BaseURL   string                         `json:"openai_base_url"`
		Providers map[string]modelProviderConfig `json:"model_providers"`
	}
	if json.Unmarshal(cfg.Config, &config) != nil {
		return modelProviderConfig{}, false
	}
	if config.Provider == "" {
		config.Provider = "openai"
	}
	p := config.Providers[config.Provider]
	return p, (config.Provider != "openai" && config.Provider != "amazon-bedrock") || p.BaseURL != "" || config.BaseURL != ""
}

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func providerEnvironmentKey(key string) bool {
	if !environmentName.MatchString(key) {
		return false
	}
	// A provider reference is not permission to change process loading,
	// configuration roots, executable search paths or shell startup behavior.
	for _, prefix := range []string{"CODEX_", "YIP_", "LD_", "DYLD_", "XDG_", "NODE_", "BUN_", "PYTHON", "RUBY", "PERL", "GIT_", "BASH_"} {
		if strings.HasPrefix(key, prefix) {
			return false
		}
	}
	switch key {
	case "PATH", "HOME", "USER", "LOGNAME", "SHELL", "ENV", "BASH_ENV", "ZDOTDIR", "TMPDIR", "TMP", "TEMP", "IFS":
		return false
	}
	return true
}

func providerEnvironment(env []string, cfg configReadResponse, lookup func(string) (string, bool)) []string {
	p, _ := selectedProvider(cfg)
	keys := []string{p.EnvKey}
	for _, key := range p.EnvHTTPHeaders {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := append([]string(nil), env...)
	seen := map[string]bool{}
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		seen[key] = true
	}
	for _, key := range keys {
		if seen[key] || !providerEnvironmentKey(key) {
			continue
		}
		seen[key] = true
		if value, ok := lookup(key); ok {
			out = append(out, key+"="+value)
		}
	}
	return out
}

// Resolve references using the harness's own configuration precedence in an
// empty directory. Project config cannot request ambient runner secrets.
// Explicit environments (including container workers) are already authoritative.
func (a *Adapter) providerEnv(ctx context.Context, exe string, env []string) ([]string, error) {
	if env != nil {
		return env, nil
	}
	env = providers.BaseEnv(envKeys)
	dir, err := os.MkdirTemp("", "yip-codex-config-")
	if err != nil {
		return nil, errors.New("could not create the model provider inspection directory")
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, a.timeouts.Handshake)
	defer cancel()
	srv, err := a.launch(ctx, exe, dir, env)
	if err != nil {
		return nil, errors.New("could not start the model provider configuration inspection")
	}
	defer srv.shutdown(a.timeouts.Grace)
	if err := srv.initialize(ctx); err != nil {
		return nil, errors.New("could not initialize the model provider configuration inspection")
	}
	var cfg configReadResponse
	if err := srv.c.call(ctx, methodConfigRead, configReadParams{Cwd: dir}, &cfg); err != nil {
		return nil, errors.New("could not read the effective model provider configuration")
	}
	return providerEnvironment(env, cfg, os.LookupEnv), nil
}

func classifyConfiguredAccount(account getAccountResponse, cfg configReadResponse, env []string) (state, detail, label, billing string) {
	state, detail, label, billing = classifyAccount(account)
	p, custom := selectedProvider(cfg)
	if p.EnvKey != "" {
		present := false
		for _, entry := range env {
			key, value, _ := strings.Cut(entry, "=")
			if key == p.EnvKey {
				present = strings.TrimSpace(value) != ""
			}
		}
		if !present {
			return protocol.AuthNeedsSignIn, "The selected model provider requires an environment credential that is missing from the runner. Export its configured env_key for the runner process; a shell or another HOME may use a different configuration.", "", protocol.BillingUnknown
		}
		// The configured credential takes precedence over account sign-in.
		state, detail, label, billing = protocol.AuthReady, "Model provider environment credential configured locally; not remotely verified.", "", protocol.BillingAPI
	}
	if custom && state == protocol.AuthReady && (account.Account == nil || account.Account.Type != accountBedrock) {
		billing = protocol.BillingUnknown
		if p.EnvKey != "" || len(p.EnvHTTPHeaders) > 0 {
			// Environment-backed API access requires the existing API opt-in;
			// an unknown invoice price must not bypass that permission.
			billing = protocol.BillingAPI
		}
		return state, "Model gateway configured locally. Credential validity, model access, pricing and gateway reachability have not been verified.", "", billing
	}
	return
}
