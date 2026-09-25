package cursor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/providers/acp"
	"github.com/binbandit/yip/protocol"
)

// probeHandler answers everything an agent might send during a capability
// probe: no session exists, so permissions are cancelled and anything else is
// "method not found".
type probeHandler struct{}

func (probeHandler) HandleRequest(req *acp.Request) {
	if req.Method == acp.MethodRequestPermission {
		_ = req.Reply(acp.CancelledPermission())
		return
	}
	_ = req.MethodNotFound()
}

func (probeHandler) HandleNotification(string, json.RawMessage) {}

func (a *Adapter) probeInstalled(ctx context.Context, exe string, inst protocol.ProviderInstallation) protocol.ProviderInstallation {
	env := a.probeEnv
	out, errOut, err := runCmd(ctx, exe, env, 15*time.Second, "--version")
	if err != nil {
		inst.AuthState = protocol.AuthError
		inst.AuthDetail = "`" + filepath.Base(exe) + " --version` failed: " + oneLine(err.Error()+" "+errOut, 300)
		return inst
	}
	inst.Version = firstLine(out)

	// Negotiate capabilities with a short-lived `agent acp` (initialize only;
	// no session, no prompt) in an empty directory so no repository
	// configuration is loaded.
	init, err := a.probeInitialize(ctx, exe, env)
	if err != nil {
		inst.AuthState = protocol.AuthError
		inst.AuthDetail = "`" + filepath.Base(exe) + " acp` did not complete the ACP initialize handshake: " + oneLine(err.Error(), 400)
		inst.Limitations = append(inst.Limitations, "ACP handshake failed; the executable may not be the Cursor CLI or may be too old to support `agent acp`.")
		return inst
	}
	caps := protocol.ProviderCapabilities{
		StructuredEvents: true,
		ToolApprovals:    true,
		UserQuestions:    true,
		SessionResume:    init.AgentCapabilities.LoadSession || init.AgentCapabilities.CanResume(),
		ActiveSteering:   false,
		UsageTelemetry:   false,
		Sandbox:          false,
		ReadOnly:         true,
		MCPTools:         true,
	}
	if init.AgentInfo != nil && init.AgentInfo.Version != "" && inst.Version == "" {
		inst.Version = init.AgentInfo.Version
	}
	if !hasAuthMethod(init, cursorAuthMethod) {
		inst.Limitations = append(inst.Limitations, "The agent did not advertise the documented \"cursor_login\" ACP auth method; sessions rely on the existing CLI login.")
	}
	if !caps.SessionResume {
		inst.Limitations = append(inst.Limitations, "The agent did not advertise session/load; runs cannot resume a previous Cursor session and start fresh instead.")
	}
	inst.Limitations = append(inst.Limitations,
		"Steering input is queued and sent as the next prompt turn; ACP cannot inject input into a running turn.",
		"Read-only and conversation runs use Cursor's documented \"ask\" mode plus yip-side denial of edits, commands and mode switches; Start refuses when the session does not offer ask mode.",
		"The yip MCP bridge is passed through ACP session/new mcpServers; Cursor's documentation only describes MCP servers from .cursor/mcp.json, so bridge availability in Cursor is unverified.",
		"Usage and cost appear only if Cursor reports them over ACP; billing is shown as unknown.",
	)

	// Authentication status via the documented `agent status` command.
	state, detail, account := a.probeAuth(ctx, exe, env)
	inst.AuthState, inst.AuthDetail, inst.Account = state, detail, account

	// Model catalogue via the documented `agent models` command.
	if models := a.probeModels(ctx, exe, env); len(models) > 0 {
		inst.Models = models
		caps.ModelEnumeration = true
	} else {
		inst.Limitations = append(inst.Limitations, "`agent models` returned no parseable model list; the models Cursor reports inside each ACP session are still validated at start.")
	}

	// User-level CLI config can pre-approve actions so Cursor never asks yip.
	if cfg, ok := a.readUserConfig(); ok {
		switch cfg.ApprovalMode {
		case "", "allowlist":
		case "unrestricted":
			caps.ToolApprovals = false
			inst.Limitations = append(inst.Limitations, "Cursor's user config sets approvalMode \"unrestricted\": Cursor runs tools without asking, so yip approvals are bypassed on this machine.")
		default:
			inst.Limitations = append(inst.Limitations, "Cursor's user config sets approvalMode \""+cfg.ApprovalMode+"\": Cursor may approve some actions itself without asking yip.")
		}
		if n := len(cfg.Permissions.Allow); n > 0 {
			inst.Limitations = append(inst.Limitations, "Cursor's user config pre-approves "+itoa(n)+" permission rule(s) (permissions.allow); matching actions run without a yip approval request.")
		}
	}
	inst.Capabilities = caps
	return inst
}

func (a *Adapter) probeInitialize(ctx context.Context, exe string, env []string) (acp.InitializeResult, error) {
	dir, err := os.MkdirTemp("", "yip-cursor-probe-")
	if err != nil {
		return acp.InitializeResult{}, err
	}
	defer os.RemoveAll(dir)
	ap, err := launch(exe, dir, env, probeHandler{})
	if err != nil {
		return acp.InitializeResult{}, err
	}
	defer ap.stop(a.eofWait, a.termGrace)
	ictx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	init, err := ap.initialize(ictx)
	if err != nil {
		ap.waitExit(time.Second)
		if tail := strings.TrimSpace(ap.stderr.String()); tail != "" {
			return init, errorf("%v (stderr: %s)", err, lastLines(tail, 4, 400))
		}
	}
	return init, err
}

// probeAuth reads `agent status --format json` (documented; the JSON shape is
// not, so recognised keys are matched leniently) and falls back to the text
// form. It never reads credential files.
func (a *Adapter) probeAuth(ctx context.Context, exe string, env []string) (state, detail, account string) {
	const signIn = "Cursor CLI is installed but not signed in on this machine. Run `agent login` in a terminal on this runner (set NO_OPEN_BROWSER=1 to get a URL instead of a browser), or provide CURSOR_API_KEY in the runner's allowlisted environment. yip never automates the browser login."
	out, errOut, err := runCmd(ctx, exe, env, 20*time.Second, "status", "--format", "json")
	if st, acct, ok := parseStatusJSON(out); ok {
		if st == protocol.AuthNeedsSignIn {
			return st, signIn, ""
		}
		return st, "Signed in to Cursor (per `agent status`).", acct
	}
	text := out + "\n" + errOut
	if err != nil && strings.TrimSpace(out) == "" {
		// Older CLIs may not support --format; retry the text form.
		o2, e2, err2 := runCmd(ctx, exe, env, 20*time.Second, "status")
		text, err = o2+"\n"+e2, err2
	}
	low := strings.ToLower(text)
	switch {
	case containsAny(low, []string{"not authenticated", "not logged in", "unauthenticated", "logged out", "please log in", "login required"}):
		return protocol.AuthNeedsSignIn, signIn, ""
	case containsAny(low, []string{"logged in as", "authenticated as", "signed in as"}):
		return protocol.AuthReady, "Signed in to Cursor (per `agent status`).", accountFromText(text)
	}
	if err != nil {
		return protocol.AuthUnknown, "Could not determine Cursor sign-in state: `agent status` failed: " + oneLine(err.Error()+" "+text, 300), ""
	}
	return protocol.AuthUnknown, "Could not determine Cursor sign-in state from `agent status` output; a run will report auth_required if the CLI is not signed in.", ""
}

var authBoolKeys = map[string]bool{"authenticated": true, "isauthenticated": true, "loggedin": true, "isloggedin": true, "signedin": true, "issignedin": true}
var accountKeys = map[string]bool{"email": true, "useremail": true, "account": true, "username": true}

func parseStatusJSON(out string) (state, account string, ok bool) {
	var v any
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &v) != nil {
		return "", "", false
	}
	var authed *bool
	var walk func(x any, depth int)
	walk = func(x any, depth int) {
		m, isMap := x.(map[string]any)
		if !isMap || depth > 3 {
			return
		}
		for k, val := range m {
			lk := strings.ToLower(k)
			switch t := val.(type) {
			case bool:
				if authBoolKeys[lk] && authed == nil {
					b := t
					authed = &b
				}
			case string:
				if accountKeys[lk] && account == "" {
					account = t
				}
				if (lk == "status" || lk == "authstatus" || lk == "state") && authed == nil {
					ls := strings.ToLower(t)
					switch {
					case strings.Contains(ls, "not") || strings.Contains(ls, "unauth") || strings.Contains(ls, "logged out") || strings.Contains(ls, "logged_out"):
						b := false
						authed = &b
					case strings.Contains(ls, "authenticated") || strings.Contains(ls, "logged in") || strings.Contains(ls, "logged_in"):
						b := true
						authed = &b
					}
				}
			case map[string]any:
				walk(t, depth+1)
			}
		}
	}
	walk(v, 0)
	if authed == nil {
		return "", "", false
	}
	if *authed {
		return protocol.AuthReady, account, true
	}
	return protocol.AuthNeedsSignIn, "", true
}

var asAccount = regexp.MustCompile(`(?i)(?:logged|signed|authenticated) in as\s+(\S+)`)

func accountFromText(s string) string {
	if m := asAccount.FindStringSubmatch(s); len(m) == 2 {
		return strings.Trim(m[1], ".,()<>")
	}
	return ""
}

var proseWords = map[string]bool{"available": true, "tip": true, "use": true, "loading": true, "models": true, "no": true, "error": true, "run": true}

var (
	ansiRE    = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	modelIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/\-]*(\[[^\]\s]*\])?$`)
)

// probeModels lists models with the documented `agent models` command. Its
// output format is not documented, so parsing is best-effort: one model per
// line, "<id> - <label>" or "<id>  <label>", with "(current)"/"(default)"
// markers.
func (a *Adapter) probeModels(ctx context.Context, exe string, env []string) []protocol.Model {
	out, _, err := runCmd(ctx, exe, env, 30*time.Second, "models")
	if err != nil {
		return nil
	}
	return parseModels(out)
}

func parseModels(out string) []protocol.Model {
	var models []protocol.Model
	seen := map[string]bool{}
	for _, raw := range strings.Split(ansiRE.ReplaceAllString(out, ""), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasSuffix(line, ":") {
			continue
		}
		isDefault := false
		for _, mark := range []string{"*", "✓", "✔", ">", "•", "-"} {
			if strings.HasPrefix(line, mark+" ") {
				if mark != "-" && mark != "•" {
					isDefault = true
				}
				line = strings.TrimSpace(strings.TrimPrefix(line, mark))
			}
		}
		low := strings.ToLower(line)
		if strings.Contains(low, "(current)") || strings.Contains(low, "(default)") {
			isDefault = true
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		id := fields[0]
		if !modelIDRE.MatchString(id) || !strings.ContainsAny(id, "abcdefghijklmnopqrstuvwxyz") || seen[id] {
			continue
		}
		label := strings.TrimSpace(strings.TrimPrefix(line, id))
		label = strings.TrimSpace(strings.TrimLeft(label, "-–—:"))
		for _, m := range []string{"(current)", "(default)", "(Current)", "(Default)"} {
			label = strings.TrimSpace(strings.ReplaceAll(label, m, ""))
		}
		if label == "" {
			label = id
		}
		// Skip prose lines such as "Available models" or "Tip: ...".
		if proseWords[strings.ToLower(id)] || strings.HasSuffix(id, ":") {
			continue
		}
		seen[id] = true
		models = append(models, protocol.Model{ID: id, Label: label, Default: isDefault})
		if len(models) >= 200 {
			break
		}
	}
	return models
}

// userConfig is the subset of the documented cli-config.json that affects
// whether Cursor asks before acting
// (https://cursor.com/docs/cli/reference/configuration). Only these keys are
// decoded; nothing else from the file is read or logged.
type userConfig struct {
	ApprovalMode string `json:"approvalMode"`
	Permissions  struct {
		Allow []string `json:"allow"`
	} `json:"permissions"`
}

func (a *Adapter) readUserConfig() (userConfig, bool) {
	dir := a.userConfigDir
	if dir == "" {
		dir = os.Getenv("CURSOR_CONFIG_DIR")
	}
	if dir == "" {
		h := homeDir()
		if h == "" {
			return userConfig{}, false
		}
		dir = filepath.Join(h, ".cursor")
	}
	b, err := os.ReadFile(filepath.Join(dir, "cli-config.json"))
	if err != nil {
		return userConfig{}, false
	}
	var c userConfig
	if json.Unmarshal(b, &c) != nil {
		return userConfig{}, false
	}
	return c, true
}

func firstLine(s string) string {
	s = strings.TrimSpace(ansiRE.ReplaceAllString(s, ""))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
