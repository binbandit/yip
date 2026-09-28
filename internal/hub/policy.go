package hub

import (
	"path"
	"regexp"
	"strings"
)

// Command classes, most severe first. A command line can fall into several
// (for example `git push && curl …`); evaluatePolicy checks each against the
// run's grants.
const (
	classReview   = "review"  // publishing a review outside yip: always refused
	classExec     = "exec"    // dangerous or uninspectable: exceptional approval
	classInline   = "inline"  // code given on the command line: exceptional, except in an isolated check
	classNetwork  = "network" // arbitrary network access: exceptional approval
	classMerge    = "merge"
	classPush     = "push"
	classOpenPR   = "open_pr"
	classLocation = "location" // intermediate: a directory change that cannot inherit repository grants
)

var classOrder = []string{classReview, classExec, classInline, classNetwork, classMerge, classPush, classOpenPR}

// cmdClass is one reason a command needs more than routine permission.
type cmdClass struct {
	Class string
	Why   string
}

// inlineFlags are the flags with which each interpreter runs code given on
// the command line rather than from a file in the workspace.
var inlineFlags = map[string][]string{
	"python": {"c"}, "node": {"e", "p", "eval", "print"}, "nodejs": {"e", "p", "eval", "print"},
	"deno": {"eval"}, "bun": {"e", "eval", "print"}, "perl": {"e", "E"}, "ruby": {"e"}, "php": {"r"},
	"lua": {"e"}, "osascript": {"e"}, "Rscript": {"e"},
}

var (
	reURL        = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*://|^[\w.-]+@[\w.-]+:`)
	reAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
)

// classifyCommand parses a shell command line into simple commands and
// classifies each by what it does. It is a policy aid, not a sandbox: any
// command that runs workspace code (a test suite, a build script) can do
// what that code does, which is why checks also run with a scratch HOME and
// no credentials. Its job is to make explicit actions, however they are
// spelled (`git -C . push`, `/usr/bin/git push`, `sh -c "…"`, `$(…)`),
// reach the same decision.
func classifyCommand(line string) []cmdClass { return classifyCommandFor(line, "") }

// classifyCommandFor classifies a command run for work whose GitHub
// repository is repo ("owner/name", or empty): naming that repository
// explicitly is not selecting another one.
func classifyCommandFor(line, repo string) []cmdClass {
	var out []cmdClass
	add := func(class, why string) {
		for _, c := range out {
			if c.Class == class {
				return
			}
		}
		out = append(out, cmdClass{class, why})
	}
	cmds, dynamic, ok := splitShell(line)
	if !ok {
		add(classExec, "the command line could not be parsed")
		return out
	}
	if dynamic != "" {
		add(classExec, "it uses "+dynamic+", which yip can't inspect")
	}
	for _, sc := range cmds {
		for _, r := range sc.redirects {
			if writesOutside(r) {
				add(classExec, "it writes outside the workspace ("+r+")")
			}
		}
		classifyArgv(sc.argv, repo, add)
	}
	privileged, changedDirectory := false, false
	for _, c := range out {
		privileged = privileged || c.Class == classPush || c.Class == classMerge || c.Class == classOpenPR
		changedDirectory = changedDirectory || c.Class == classLocation
	}
	if privileged && changedDirectory {
		add(classExec, "it changes directories before using a repository grant")
	}
	filtered := out[:0]
	for _, c := range out {
		if c.Class != classLocation {
			filtered = append(filtered, c)
		}
	}
	out = filtered
	sortClasses(out)
	return out
}

func sortClasses(cs []cmdClass) {
	rank := func(c string) int {
		for i, o := range classOrder {
			if o == c {
				return i
			}
		}
		return len(classOrder)
	}
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0 && rank(cs[j].Class) < rank(cs[j-1].Class); j-- {
			cs[j], cs[j-1] = cs[j-1], cs[j]
		}
	}
}

func classifyArgv(argv []string, repo string, add func(class, why string)) {
	// Leading assignments (FOO=1 cmd) and wrappers run the real program.
	for len(argv) > 0 {
		a := argv[0]
		if reAssignment.MatchString(a) {
			name, value, _ := strings.Cut(a, "=")
			switch {
			case name == "PATH" || name == "LD_PRELOAD" || strings.HasPrefix(name, "DYLD_") ||
				strings.HasPrefix(name, "GIT_SSH") || strings.HasPrefix(name, "GIT_CONFIG") || name == "GIT_EXEC_PATH":
				add(classExec, "it overrides "+name)
			case name == "GIT_DIR" || name == "GIT_COMMON_DIR":
				if !currentGitLocation("--git-dir", value) {
					add(classExec, "it selects another git directory through "+name)
				}
			case name == "GIT_WORK_TREE":
				if !currentGitLocation("--work-tree", value) {
					add(classExec, "it selects another working tree through "+name)
				}
			case name == "GH_REPO" || name == "GH_HOST" || name == "GIT_CEILING_DIRECTORIES" || name == "GIT_DISCOVERY_ACROSS_FILESYSTEM":
				add(classExec, "it overrides the repository through "+name)
			}
			argv = argv[1:]
			continue
		}
		break
	}
	if len(argv) == 0 {
		return
	}
	prog := argv[0]
	if strings.ContainsAny(prog, "$`") {
		add(classExec, "the program name is computed at run time")
		return
	}
	base := path.Base(prog)
	args := argv[1:]
	for _, a := range args {
		if reURL.MatchString(a) {
			add(classNetwork, "it contacts "+truncate(a, 60))
			break
		}
	}
	switch base {
	case "env":
		for len(args) > 0 && strings.HasPrefix(args[0], "-") {
			flag, value, joined := strings.Cut(args[0], "=")
			args = args[1:]
			if flag == "--" {
				break
			}
			switch flag {
			case "-u", "--unset", "-C", "--chdir", "-S", "--split-string":
				if !joined && len(args) > 0 {
					value, args = args[0], args[1:]
				}
				if flag == "-C" || flag == "--chdir" {
					if !currentGitLocation("-C", value) {
						add(classExec, "env selects another working directory")
					}
				}
				if flag == "-S" || flag == "--split-string" {
					add(classInline, "env interprets a command string")
					for _, c := range classifyCommandFor(value, repo) {
						add(c.Class, c.Why)
					}
				}
			default:
				if flag != "-i" && flag != "--ignore-environment" && flag != "-0" && flag != "--null" && flag != "-v" && flag != "--debug" {
					add(classExec, "env uses an option whose command or directory yip cannot bind")
				}
			}
		}
		classifyArgv(args, repo, add)
	case "command", "builtin", "nohup", "time", "exec", "caffeinate", "stdbuf", "ionice":
		classifyArgv(skipFlags(args), repo, add)
	case "export", "readonly", "declare", "typeset", "local":
		for _, assignment := range args {
			if reAssignment.MatchString(assignment) {
				classifyArgv([]string{assignment}, repo, add)
			}
		}
	case "cd", "pushd":
		for _, directory := range operands(args) {
			if !currentGitLocation("-C", directory) {
				add(classLocation, "it changes the working directory")
			}
			if path.IsAbs(directory) || outsideWorkspace(directory) || strings.ContainsAny(directory, "$`") {
				add(classExec, "it changes to a directory outside the workspace")
			}
		}
		if len(operands(args)) == 0 {
			add(classExec, "it changes to an unspecified working directory")
		}
	case "popd":
		add(classLocation, "it restores an unspecified working directory")
	case "nice":
		if len(args) >= 2 && args[0] == "-n" {
			args = args[2:]
		}
		classifyArgv(skipFlags(args), repo, add)
	case "timeout", "gtimeout":
		rest := skipFlags(args)
		if len(rest) > 0 {
			rest = rest[1:] // the duration
		}
		classifyArgv(rest, repo, add)
	case "xargs":
		i := 0
		for i < len(args) && strings.HasPrefix(args[i], "-") {
			switch args[i] {
			case "-n", "-L", "-P", "-I", "-s", "-E", "-d", "-a", "-J", "-R":
				i++
			}
			i++
		}
		if i < len(args) {
			classifyArgv(args[i:], repo, add)
		}
	case "find":
		for i, a := range args {
			if a == "-exec" || a == "-execdir" || a == "-ok" || a == "-okdir" {
				if a == "-execdir" || a == "-okdir" {
					add(classLocation, "find changes the working directory")
				}
				var sub []string
				for _, b := range args[i+1:] {
					if b == ";" || b == `\;` || b == "+" {
						break
					}
					sub = append(sub, b)
				}
				classifyArgv(sub, repo, add)
			}
			if a == "-delete" {
				for _, p := range args {
					if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") {
						add(classExec, "it deletes files outside the workspace")
					}
				}
			}
		}
	case "sudo", "su", "doas", "pkexec", "runas":
		add(classExec, "it runs "+base)
	case "eval", "source", ".":
		if base == "eval" {
			add(classInline, "it evaluates generated shell code")
		}
	case "sh", "bash", "zsh", "dash", "ksh", "fish", "csh", "tcsh", "pwsh", "powershell":
		if inlineCode(args, "c", "command") || len(operands(args)) == 0 {
			add(classInline, "it runs inline shell code")
			// Classify the inline script too, so its own actions (a push, a
			// merge) are named in the request.
			if code := operands(args); len(code) > 0 {
				for _, c := range classifyCommandFor(code[0], repo) {
					add(c.Class, c.Why)
				}
			}
		}
	case "python", "python2", "python3", "node", "nodejs", "deno", "bun", "perl", "ruby", "php", "lua", "osascript", "Rscript", "irb", "jshell":
		interp := strings.TrimRight(base, "0123456789")
		if inlineCode(args, inlineFlags[interp]...) || (len(args) > 0 && args[0] == "-") ||
			(interp != "deno" && interp != "bun" && len(args) == 0) {
			add(classInline, "it runs inline "+base+" code")
		}
	case "curl", "wget", "nc", "ncat", "netcat", "socat", "telnet", "ftp", "tftp", "aria2c", "httpie", "http", "https", "xh":
		add(classNetwork, "it uses "+base)
	case "ssh", "scp", "sftp", "rsync", "mosh":
		add(classExec, "it uses "+base)
	case "mkfs", "dd", "shutdown", "reboot", "halt", "poweroff", "launchctl", "systemctl", "crontab", "diskutil", "kubectl",
		"helm", "security", "defaults", "chflags", "mount", "umount", "iptables", "pfctl":
		add(classExec, "it runs "+base)
	case "rm", "chmod", "chown", "chgrp", "mv", "cp", "ln", "tee", "truncate", "shred":
		for _, a := range operands(args) {
			if outsideWorkspace(a) {
				add(classExec, base+" touches "+truncate(a, 60)+" outside the workspace")
				break
			}
		}
	case "npm", "pnpm", "yarn", "cargo", "gem", "twine", "poetry", "flit", "dotnet", "mvn", "gradle":
		if w := words(args, nil); len(w) > 0 && (w[0] == "publish" || w[0] == "upload" || w[0] == "push" || w[0] == "deploy" ||
			(base == "npm" && w[0] == "unpublish") || (base == "dotnet" && len(w) > 1 && w[0] == "nuget" && w[1] == "push")) {
			add(classExec, "it publishes a package")
		}
	case "docker", "podman", "buildah":
		if w := words(args, nil); len(w) > 0 && (w[0] == "push" || w[0] == "login" || (len(w) > 1 && w[0] == "image" && w[1] == "push")) {
			add(classExec, "it pushes an image")
		}
	case "terraform", "tofu", "pulumi":
		if w := words(args, nil); len(w) > 0 && (w[0] == "apply" || w[0] == "destroy" || w[0] == "up" || w[0] == "import") {
			add(classExec, "it changes infrastructure")
		}
	case "git":
		classifyGit(args, add)
	case "gh":
		classifyGH(args, repo, add)
	case "hub":
		if w := words(args, nil); len(w) > 0 && (w[0] == "push" || w[0] == "pull-request" || w[0] == "merge" || w[0] == "api") {
			add(classExec, "it uses the hub CLI to change the remote")
		}
	}
}

// classifyGit skips git's global options to find the subcommand.
func classifyGit(args []string, add func(class, why string)) {
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "-C" || a == "--git-dir" || a == "--work-tree":
			value := ""
			if i+1 < len(args) {
				value = args[i+1]
			}
			classifyGitLocation(a, value, add)
			i += 2
			continue
		case strings.HasPrefix(a, "-C") && len(a) > 2:
			classifyGitLocation("-C", a[2:], add)
		case strings.HasPrefix(a, "--git-dir=") || strings.HasPrefix(a, "--work-tree="):
			flag, value, _ := strings.Cut(a, "=")
			classifyGitLocation(flag, value, add)
		case a == "--namespace" || a == "--super-prefix":
			i += 2
			continue
		case a == "--config-env":
			add(classExec, "it overrides git configuration from the environment")
			i += 2
			continue
		case a == "-c":
			if i+1 < len(args) {
				key := strings.ToLower(args[i+1])
				if dangerousGitConfig(key) {
					add(classExec, "it overrides git's "+strings.SplitN(key, "=", 2)[0])
				}
			}
			i += 2
			continue
		case strings.HasPrefix(a, "-c") && len(a) > 2:
			if dangerousGitConfig(strings.ToLower(a[2:])) {
				add(classExec, "it overrides git configuration")
			}
		case strings.HasPrefix(a, "--config-env=") || strings.HasPrefix(a, "--exec-path"):
			add(classExec, "it overrides git configuration")
		case strings.HasPrefix(a, "-"):
		default:
			goto sub
		}
		i++
	}
	return
sub:
	sub, rest := args[i], args[i+1:]
	if strings.ContainsAny(sub, "$`") {
		add(classExec, "the git subcommand is computed at run time")
		return
	}
	switch sub {
	case "push", "send-pack", "send-email", "request-pull":
		add(classPush, "it pushes to a remote")
		classifyGitPushTarget(rest, add)
	case "config":
		for _, a := range rest {
			if dangerousGitConfig(strings.ToLower(a)) {
				add(classExec, "it changes git configuration that runs commands")
				break
			}
		}
	case "remote":
		if w := words(rest, nil); len(w) > 0 && (w[0] == "add" || w[0] == "set-url") {
			add(classNetwork, "it adds or changes a remote")
		}
	case "submodule":
		if w := words(rest, nil); len(w) > 0 && (w[0] == "add" || w[0] == "update" || w[0] == "sync") {
			add(classNetwork, "it fetches submodules")
		}
	case "filter-branch", "filter-repo":
		add(classExec, "it rewrites history")
	}
}

// The hub cannot resolve a runner's filesystem. Only explicit spellings of
// the current checkout can inherit its project grant; other selectors need
// an exact-action decision, even when their paths are under a temp directory.
func classifyGitLocation(flag, value string, add func(class, why string)) {
	if !currentGitLocation(flag, value) {
		add(classExec, "it selects another git checkout with "+flag)
	}
}

func currentGitLocation(flag, value string) bool {
	// Do not normalize paths: link/.. can leave the checkout via a symlink.
	if flag == "--git-dir" {
		return value == ".git" || value == "./.git"
	}
	return value == "." || value == "./"
}

func classifyGitPushTarget(args []string, add func(class, why string)) {
	firstOperand, explicitRepo := "", false
	flags := true
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !flags {
			if firstOperand == "" {
				firstOperand = arg
			}
			continue
		}
		switch {
		case arg == "--":
			flags = false
		case arg == "--repo":
			explicitRepo = true
			if i+1 >= len(args) || args[i+1] != "origin" {
				add(classExec, "it selects a push destination outside the assigned origin")
			}
			i++
		case strings.HasPrefix(arg, "--repo="):
			explicitRepo = true
			if strings.TrimPrefix(arg, "--repo=") != "origin" {
				add(classExec, "it selects a push destination outside the assigned origin")
			}
		case arg == "--receive-pack" || arg == "--exec" || strings.HasPrefix(arg, "--receive-pack=") || strings.HasPrefix(arg, "--exec="):
			add(classExec, "it overrides the remote push command")
		case arg == "-o" || arg == "--push-option":
			i++
		case strings.HasPrefix(arg, "-"):
		default:
			if firstOperand == "" {
				firstOperand = arg
			}
		}
	}
	if !explicitRepo && firstOperand != "" && firstOperand != "origin" {
		add(classExec, "it selects a push destination outside the assigned origin")
	}
}

func dangerousGitConfig(kv string) bool {
	key := strings.SplitN(kv, "=", 2)[0]
	return strings.HasPrefix(key, "alias.") || strings.HasPrefix(key, "credential") || key == "core.hookspath" ||
		key == "core.sshcommand" || key == "core.fsmonitor" || key == "core.pager" || key == "core.editor" ||
		key == "sequence.editor" || key == "core.worktree" || strings.HasPrefix(key, "url.") || strings.HasSuffix(key, ".insteadof") ||
		strings.HasPrefix(key, "filter.") || strings.HasPrefix(key, "diff.") && strings.HasSuffix(key, ".textconv") ||
		strings.HasSuffix(key, ".command") || strings.HasPrefix(key, "protocol.") || strings.HasPrefix(key, "include") ||
		strings.HasPrefix(key, "remote.") || strings.HasPrefix(key, "branch.") && (strings.HasSuffix(key, ".remote") || strings.HasSuffix(key, ".pushremote"))
}

// classifyGH maps GitHub CLI subcommands onto grants. Reads are routine;
// anything that changes the remote needs the matching grant or approval.
func classifyGH(args []string, repo string, add func(class, why string)) {
	w := words(args, map[string]bool{"-R": true, "--repo": true, "--hostname": true})
	if len(w) == 0 {
		return
	}
	area, verb := w[0], ""
	if len(w) > 1 {
		verb = w[1]
	}
	if strings.ContainsAny(area+verb, "$`") {
		add(classExec, "the gh command is computed at run time")
		return
	}
	if area == "pr" && (verb == "create" || verb == "merge") {
		for i, arg := range args {
			selected, named := "", true
			switch {
			case arg == "--repo" || arg == "-R":
				if i+1 < len(args) {
					selected = args[i+1]
				}
			case strings.HasPrefix(arg, "--repo="):
				selected = strings.TrimPrefix(arg, "--repo=")
			case strings.HasPrefix(arg, "-R"):
				selected = strings.TrimPrefix(arg, "-R")
			case arg == "--hostname" || strings.HasPrefix(arg, "--hostname="):
			default:
				named = false
			}
			if named && (repo == "" || !strings.EqualFold(strings.TrimSuffix(selected, ".git"), repo)) {
				add(classExec, "it selects a GitHub repository outside the assigned checkout")
			}
		}
	}
	switch area {
	case "pr":
		switch verb {
		case "create":
			add(classOpenPR, "it opens a pull request")
		case "merge":
			add(classMerge, "it merges a pull request")
		case "review":
			add(classReview, "reviews are published through forge_publish_review so they stay revision-bound")
		case "view", "list", "diff", "checks", "status", "checkout", "":
		default:
			add(classExec, "it changes a pull request (gh pr "+verb+")")
		}
	case "issue", "run", "workflow", "release", "repo", "label", "gist", "search", "cache", "variable", "secret", "ruleset", "project":
		switch verb {
		case "view", "list", "status", "diff", "watch", "":
		default:
			add(classExec, "it changes the remote (gh "+area+" "+verb+")")
		}
	case "api":
		add(classNetwork, "it calls the GitHub API directly")
	case "auth", "ssh-key", "gpg-key", "extension", "alias", "config", "codespace":
		add(classExec, "it changes GitHub CLI or account settings")
	}
}

// words returns non-flag arguments, skipping the values of flags in valued.
func words(args []string, valued map[string]bool) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			if valued[a] {
				i++
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

// operands returns arguments that aren't flags.
func operands(args []string) []string { return words(args, nil) }

// skipFlags drops leading flags. Keep assignments for classifyArgv to inspect.
func skipFlags(args []string) []string {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		if args[0] == "--" {
			return args[1:]
		}
		args = args[1:]
	}
	return args
}

// inlineCode reports a flag that passes code on the command line, including
// combined short flags like -lc or -ec.
func inlineCode(args []string, flags ...string) bool {
	for _, a := range args {
		if a == "--" || !strings.HasPrefix(a, "-") {
			if !strings.HasPrefix(a, "-") {
				return false // flags after the script belong to the script
			}
			continue
		}
		name := strings.TrimLeft(a, "-")
		name, _, _ = strings.Cut(name, "=")
		for _, f := range flags {
			if name == f {
				return true
			}
			if len(f) == 1 && !strings.HasPrefix(a, "--") && strings.Contains(name, f) {
				return true
			}
		}
	}
	return false
}

// outsideWorkspace reports a path argument that leaves the working tree.
func outsideWorkspace(p string) bool {
	if strings.HasPrefix(p, "~") || strings.HasPrefix(p, "$HOME") || strings.HasPrefix(p, "${HOME}") {
		return true
	}
	if strings.HasPrefix(p, "/") {
		for _, ok := range []string{"/dev/null", "/dev/stdout", "/dev/stderr", "/tmp/", "/private/tmp/", "/var/folders/"} {
			if p == strings.TrimSuffix(ok, "/") || strings.HasPrefix(p, ok) {
				return false
			}
		}
		return true
	}
	clean := path.Clean(p)
	return clean == ".." || strings.HasPrefix(clean, "../")
}

func writesOutside(target string) bool {
	if strings.HasPrefix(target, "&") {
		return false // fd duplication (2>&1)
	}
	return outsideWorkspace(target)
}

type simpleCmd struct {
	argv      []string
	redirects []string // redirection targets
}

// splitShell tokenizes a POSIX-style command line into simple commands.
// The commands inside a command or process substitution are returned
// alongside the rest, and the substitution's output stands in the word as
// an unknown value, like a variable. dynamic names the first construct whose
// effect yip still can't inspect (a here-string, a substitution inside heredoc
// data or arithmetic). Heredoc data is consumed separately, so writing shell
// examples is not executing them.
func splitShell(s string) (cmds []simpleCmd, dynamic string, ok bool) {
	var cur simpleCmd
	type heredoc struct {
		end          string
		quoted, tabs bool
	}
	var docs []heredoc
	var tok strings.Builder
	inTok, redirNext := false, false
	flushTok := func() {
		if !inTok {
			return
		}
		t := tok.String()
		if redirNext {
			cur.redirects = append(cur.redirects, t)
			redirNext = false
		} else {
			cur.argv = append(cur.argv, t)
		}
		tok.Reset()
		inTok = false
	}
	flushCmd := func() {
		flushTok()
		if len(cur.argv) > 0 || len(cur.redirects) > 0 {
			cmds = append(cmds, cur)
		}
		cur = simpleCmd{}
	}
	mark := func(what string) {
		if dynamic == "" {
			dynamic = what
		}
	}
	// expand replaces the substitution starting at s[i] with an unknown
	// value, classifying the commands it runs. It returns where to continue.
	expand := func(i int) (int, bool) {
		next, body, arithmetic, ok := substitution(s, i)
		if !ok {
			return 0, false
		}
		if arithmetic {
			// Arithmetic runs no command unless it nests a substitution.
			if strings.Contains(body, "$(") || strings.Contains(body, "`") {
				mark("a command substitution inside arithmetic")
			}
		} else {
			inner, innerDynamic, innerOK := splitShell(body)
			if !innerOK {
				return 0, false
			}
			cmds = append(cmds, inner...)
			if innerDynamic != "" {
				mark(innerDynamic)
			}
		}
		tok.WriteString("$(…)")
		inTok = true
		return next, true
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\':
			if i+1 < len(s) {
				i++
				if s[i] != '\n' {
					tok.WriteByte(s[i])
					inTok = true
				}
			}
		case c == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, "", false
			}
			tok.WriteString(s[i+1 : i+1+j])
			inTok = true
			i += j + 1
		case c == '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				switch {
				case s[i] == '\\' && i+1 < len(s):
					i++
					tok.WriteByte(s[i])
				case s[i] == '`' || s[i] == '$' && i+1 < len(s) && s[i+1] == '(':
					next, ok := expand(i)
					if !ok {
						return nil, "", false
					}
					i = next - 1
				default:
					tok.WriteByte(s[i])
				}
			}
			if i >= len(s) {
				return nil, "", false
			}
			inTok = true
		case c == '`' || (c == '$' || c == '<' || c == '>') && i+1 < len(s) && s[i+1] == '(':
			next, ok := expand(i)
			if !ok {
				return nil, "", false
			}
			i = next - 1
		case c == ' ' || c == '\t':
			flushTok()
		case c == '#' && !inTok:
			for i+1 < len(s) && s[i+1] != '\n' {
				i++
			}
		case c == '\n' || c == ';' || c == '|' || c == '&' || c == '(' || c == ')' || c == '{' && !inTok || c == '}' && !inTok:
			if c == '&' && i+1 < len(s) && s[i+1] == '>' {
				flushTok()
				redirNext = true
				i++
				if i+1 < len(s) && s[i+1] == '>' {
					i++
				}
				continue
			}
			flushCmd()
			if c == '\n' {
				for _, doc := range docs {
					found := false
					for i+1 < len(s) {
						start := i + 1
						end := strings.IndexByte(s[start:], '\n')
						if end < 0 {
							end = len(s)
						} else {
							end += start
						}
						line := s[start:end]
						if doc.tabs {
							line = strings.TrimLeft(line, "\t")
						}
						i = end
						if line == doc.end {
							found = true
							break
						}
						if !doc.quoted {
							for k := 0; k < len(line); k++ {
								if line[k] == '\\' {
									if k+1 == len(line) {
										mark("a continued heredoc line")
									}
									k++
								} else if line[k] == '`' || line[k] == '$' && k+1 < len(line) && line[k+1] == '(' {
									mark("command substitution")
								}
							}
						}
					}
					if !found {
						return nil, "", false
					}
				}
				docs = nil
			}
		case c == '>' || c == '<':
			// A preceding fd number (2>) belongs to the redirection.
			if inTok && isDigits(tok.String()) {
				tok.Reset()
				inTok = false
			}
			flushTok()
			if c == '<' && i+1 < len(s) && s[i+1] == '<' {
				i++
				if i+1 < len(s) && s[i+1] == '<' {
					mark("a here-string")
					i++ // here-string
				} else {
					doc := heredoc{}
					if i+1 < len(s) && s[i+1] == '-' {
						doc.tabs = true
						i++
					}
					for i+1 < len(s) && (s[i+1] == ' ' || s[i+1] == '\t') {
						i++
					}
					var delimiter strings.Builder
					for i+1 < len(s) && !strings.ContainsRune(" \t\n;|&()<>", rune(s[i+1])) {
						i++
						switch s[i] {
						case '\'', '"':
							quote := s[i]
							doc.quoted = true
							for i++; i < len(s) && s[i] != quote; i++ {
								// Unusual escaped double-quoted delimiters stay exceptional.
								if s[i] == '\\' && quote == '"' {
									return nil, "", false
								}
								delimiter.WriteByte(s[i])
							}
							if i == len(s) {
								return nil, "", false
							}
						case '\\':
							doc.quoted = true
							i++
							if i == len(s) || s[i] == '\n' {
								return nil, "", false
							}
							delimiter.WriteByte(s[i])
						default:
							delimiter.WriteByte(s[i])
						}
					}
					doc.end = delimiter.String()
					if doc.end == "" {
						return nil, "", false
					}
					docs = append(docs, doc)
					continue
				}
			} else if i+1 < len(s) && (s[i+1] == '>' || s[i+1] == '|') {
				i++
			}
			if i+1 < len(s) && s[i+1] == '&' {
				// >&2 duplicates a descriptor; not a file.
				i++
				for i+1 < len(s) && (s[i+1] >= '0' && s[i+1] <= '9' || s[i+1] == '-') {
					i++
				}
				continue
			}
			if c == '>' {
				redirNext = true
			} else {
				redirNext = false
				// Input redirection: consume the operand as a non-argument.
				for i+1 < len(s) && (s[i+1] == ' ' || s[i+1] == '\t') {
					i++
				}
				for i+1 < len(s) && !strings.ContainsRune(" \t\n;|&()<>", rune(s[i+1])) {
					i++
				}
			}
		default:
			tok.WriteByte(c)
			inTok = true
		}
	}
	flushCmd()
	if len(docs) > 0 {
		return nil, "", false
	}
	return cmds, dynamic, true
}

// substitution reads the command substitution (`…`, $(…)), process
// substitution (<(…), >(…)) or arithmetic ($((…))) starting at s[i]. It
// returns the index just past its closing delimiter and the text inside,
// honouring nested parentheses, quotes and escapes.
func substitution(s string, i int) (next int, body string, arithmetic, ok bool) {
	if s[i] == '`' {
		for j := i + 1; j < len(s); j++ {
			switch s[j] {
			case '\\':
				j++
			case '`':
				return j + 1, strings.ReplaceAll(s[i+1:j], "\\`", "`"), false, true
			}
		}
		return 0, "", false, false
	}
	start := i + 2
	arithmetic = s[i] == '$' && start < len(s) && s[start] == '('
	depth := 1
	for j := start; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case '\'':
			k := strings.IndexByte(s[j+1:], '\'')
			if k < 0 {
				return 0, "", false, false
			}
			j += k + 1
		case '"':
			for j++; j < len(s) && s[j] != '"'; j++ {
				if s[j] == '\\' {
					j++
				}
			}
			if j >= len(s) {
				return 0, "", false, false
			}
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				return j + 1, s[start:j], arithmetic, true
			}
		}
	}
	return 0, "", false, false
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
