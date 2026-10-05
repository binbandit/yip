package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

// Built-in Claude Code tool names on the pinned version.
const (
	toolBash         = "Bash"
	toolRead         = "Read"
	toolEdit         = "Edit"
	toolWrite        = "Write"
	toolGlob         = "Glob"
	toolGrep         = "Grep"
	toolNotebookEdit = "NotebookEdit"
	toolWebFetch     = "WebFetch"
	toolWebSearch    = "WebSearch"
)

// hookFreeSettings is passed with --settings so no hook runs even if a
// managed or flag-level source would otherwise enable one. Documented in
// permissions.md ("Pass --settings '{"disableAllHooks": true}'").
const hookFreeSettings = `{"disableAllHooks":true}`

// toolPolicy is the permission boundary for one run mode.
type toolPolicy struct {
	// permissionMode is the --permission-mode value. Never bypassPermissions.
	permissionMode string
	// tools is the exact built-in tool set (--tools). Tools not named here do
	// not exist in the session; under --restricted, command-running tools are
	// only available when named individually.
	tools []string
	// allowed are rules that run without prompting (--allowedTools).
	allowed []string
	// disallowed are deny rules (--disallowedTools). Bare names remove the
	// tool from the model's context entirely.
	disallowed []string
}

// policyFor returns the tool policy for a run mode.
//
//   - edit: acceptEdits lets Claude edit files inside the working directory
//     (confined further by --restricted) and run the common filesystem
//     commands; every other Bash command, web access, and anything outside the
//     workspace goes to the permission-prompt tool. yip's own MCP tools are
//     pre-approved because the hub authorizes each call against the run lease.
//   - readonly / conversation: no tool that can modify files or run commands
//     exists in the session (only Read, Glob, Grep), the mutating tools are
//     also denied by name, and dontAsk mode denies anything that would prompt
//     instead of asking.
func policyFor(mode, mcpName string) (toolPolicy, error) {
	yipTools := "mcp__" + mcpName
	switch mode {
	case protocol.ModeEdit:
		return toolPolicy{
			permissionMode: "acceptEdits",
			tools: []string{
				toolBash, toolRead, toolEdit, toolWrite, toolGlob, toolGrep,
				toolNotebookEdit, toolWebFetch, toolWebSearch,
			},
			allowed: []string{yipTools},
		}, nil
	case protocol.ModeReadOnly, protocol.ModeConversation:
		return toolPolicy{
			permissionMode: "dontAsk",
			tools:          []string{toolRead, toolGlob, toolGrep},
			allowed:        []string{yipTools},
			disallowed: []string{
				toolBash, toolEdit, toolWrite, toolNotebookEdit,
				toolWebFetch, toolWebSearch,
			},
		}, nil
	default:
		return toolPolicy{}, fmt.Errorf("claude: run mode %q: %w", mode, providers.ErrUnsupported)
	}
}

// launchPlan is everything needed to exec one Claude Code run.
type launchPlan struct {
	Args    []string
	TempDir string // holds the MCP config; removed when the session ends
	MCPPath string
	Resumed bool
}

var identRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// mcpConfigFile is the --mcp-config document: only the yip bridge server.
type mcpConfigFile struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
}

type mcpServerEntry struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
}

// validateSpec checks the StartSpec fields the adapter relies on.
func validateSpec(spec providers.StartSpec) (mcpName string, err error) {
	if spec.Workdir == "" || !filepath.IsAbs(spec.Workdir) {
		return "", fmt.Errorf("claude: workdir must be an absolute path, got %q", spec.Workdir)
	}
	if fi, statErr := os.Stat(spec.Workdir); statErr != nil || !fi.IsDir() {
		return "", fmt.Errorf("claude: workdir %q is not a directory", spec.Workdir)
	}
	if strings.TrimSpace(spec.Prompt) == "" {
		return "", fmt.Errorf("claude: empty prompt")
	}
	mcpName = spec.MCP.Name
	if mcpName == "" {
		mcpName = "yip"
	}
	if !identRE.MatchString(mcpName) {
		return "", fmt.Errorf("claude: invalid MCP server name %q", mcpName)
	}
	if spec.MCP.Command == "" {
		return "", fmt.Errorf("claude: the yip bridge MCP server command is required")
	}
	// Without a permission tool, print mode silently denies every prompt; yip
	// requires approvals to reach its bridge, so refuse rather than degrade.
	if spec.PermissionTool == "" || !identRE.MatchString(spec.PermissionTool) {
		return "", fmt.Errorf("claude: invalid or missing permission tool name %q", spec.PermissionTool)
	}
	return mcpName, nil
}

// buildLaunch validates spec, writes the private MCP config, and returns the
// exact CLI arguments. Every value-taking flag uses the --flag=value form so a
// value beginning with "-" can never be parsed as another option, and no
// positional argument is ever passed (the prompt travels over stdin).
func buildLaunch(spec providers.StartSpec, tempRoot string) (*launchPlan, error) {
	mcpName, err := validateSpec(spec)
	if err != nil {
		return nil, err
	}
	pol, err := policyFor(spec.Mode, mcpName)
	if err != nil {
		return nil, err
	}
	if spec.EngineerDraft {
		if spec.Mode != protocol.ModeConversation || spec.ResumeSessionID != "" || spec.InheritUserConfig {
			return nil, fmt.Errorf("%w: invalid engineer draft scope", providers.ErrUnsupported)
		}
		pol = toolPolicy{permissionMode: "dontAsk"}
	}

	dir, err := os.MkdirTemp(tempRoot, "yip-claude-")
	if err != nil {
		return nil, fmt.Errorf("claude: temp dir: %w", err)
	}
	cfg := mcpConfigFile{MCPServers: map[string]mcpServerEntry{
		mcpName: {
			Type:    "stdio",
			Command: spec.MCP.Command,
			Args:    append([]string{}, spec.MCP.Args...),
			Env:     spec.MCP.Env,
		},
	}}
	if cfg.MCPServers[mcpName].Args == nil {
		e := cfg.MCPServers[mcpName]
		e.Args = []string{}
		cfg.MCPServers[mcpName] = e
	}
	if spec.EngineerDraft {
		cfg.MCPServers = map[string]mcpServerEntry{}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	// The bridge env may carry a run token: keep it off argv and private.
	mcpPath := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(mcpPath, raw, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("claude: write mcp config: %w", err)
	}

	args := []string{
		"-p",
		"--output-format=stream-json",
		"--input-format=stream-json",
		"--verbose",
		"--include-partial-messages",
		// Startup boundary: ignore user/project/local settings files (hooks,
		// env blocks, helpers, permission rules), remove command-running tools
		// unless named in --tools, confine file tools to the working
		// directory, and refuse bypassPermissions.
		"--restricted",
		// Only the yip bridge: no .mcp.json, user, or plugin MCP servers.
		"--strict-mcp-config",
		"--mcp-config=" + mcpPath,
		// Repository skills can carry hooks and allowed-tools; disable them.
		"--disable-slash-commands",
		"--settings=" + hookFreeSettings,
		"--permission-prompt-tool=mcp__" + mcpName + "__" + spec.PermissionTool,
		"--permission-prompts=host",
		"--permission-mode=" + pol.permissionMode,
		"--tools=" + strings.Join(pol.tools, ","),
		"--allowedTools=" + strings.Join(pol.allowed, ","),
	}
	if spec.EngineerDraft {
		filtered := args[:0]
		for _, arg := range args {
			if strings.HasPrefix(arg, "--permission-prompt-tool=") || arg == "--permission-prompts=host" {
				continue
			}
			filtered = append(filtered, arg)
		}
		args = filtered
	}
	if spec.InheritUserConfig && spec.Mode == protocol.ModeEdit {
		filtered := args[:0]
		for _, arg := range args {
			switch arg {
			case "--restricted", "--strict-mcp-config", "--disable-slash-commands":
				continue
			}
			filtered = append(filtered, arg)
		}
		args = filtered
	}
	if len(pol.disallowed) > 0 {
		args = append(args, "--disallowedTools="+strings.Join(pol.disallowed, ","))
	}
	if spec.Model != "" {
		args = append(args, "--model="+spec.Model)
	}
	if spec.ResumeSessionID != "" {
		args = append(args, "--resume="+spec.ResumeSessionID)
	}
	if spec.Instructions != "" {
		args = append(args, "--append-system-prompt="+spec.Instructions)
	}
	return &launchPlan{Args: args, TempDir: dir, MCPPath: mcpPath, Resumed: spec.ResumeSessionID != ""}, nil
}
