package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

const launchdTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>dev.getyip.{{role}}</string>
  <key>ProgramArguments</key>
  <array>
{{args}}
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Background</string>
  <key>StandardOutPath</key><string>{{log}}</string>
  <key>StandardErrorPath</key><string>{{log}}</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key><string>{{path}}</string>
  </dict>
</dict>
</plist>
`

const systemdTemplate = `[Unit]
Description=yip {{role}}
After=network-online.target
Wants=network-online.target

[Service]
ExecStart={{exec}}
Restart=on-failure
RestartSec=5
Environment=PATH={{path}}
NoNewPrivileges=true

[Install]
WantedBy=default.target
`

// runService writes a launchd agent or systemd user unit. It prints the
// command that activates it rather than silently changing system state.
func runService(args []string) error {
	if len(args) < 2 || args[0] != "install" || (args[1] != "hub" && args[1] != "runner") {
		return errors.New("usage: yip service install hub|runner [--data DIR|--state DIR] [--print] [-- extra flags]")
	}
	role := args[1]
	fs := flag.NewFlagSet("service install", flag.ExitOnError)
	data := fs.String("data", defaultDataDir(), "hub data directory")
	state := fs.String("state", defaultRunnerDir(), "runner state directory")
	printOnly := fs.Bool("print", false, "print the definition instead of writing it")
	_ = fs.Parse(args[2:])
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	cmdArgs := []string{exe, role}
	if role == "hub" {
		cmdArgs = append(cmdArgs, "--data", *data)
	} else {
		cmdArgs = append(cmdArgs, "--state", *state)
	}
	cmdArgs = append(cmdArgs, fs.Args()...)
	home, _ := os.UserHomeDir()
	path := os.Getenv("PATH")
	var file, content, activate string
	switch runtime.GOOS {
	case "darwin":
		logDir := filepath.Join(home, "Library", "Logs", "yip")
		_ = os.MkdirAll(logDir, 0o700)
		var lines []string
		for _, a := range cmdArgs {
			lines = append(lines, "    <string>"+xmlEscape(a)+"</string>")
		}
		content = strings.NewReplacer("{{role}}", role, "{{args}}", strings.Join(lines, "\n"), "{{log}}", filepath.Join(logDir, role+".log"),
			"{{path}}", xmlEscape(path)).Replace(launchdTemplate)
		file = filepath.Join(home, "Library", "LaunchAgents", "dev.getyip."+role+".plist")
		activate = fmt.Sprintf("launchctl bootstrap gui/%d %s\nlaunchctl kickstart -k gui/%d/dev.getyip.%s", syscall.Getuid(), file, syscall.Getuid(), role)
	case "linux":
		quoted := make([]string, len(cmdArgs))
		for i, a := range cmdArgs {
			quoted[i] = shellQuote(a)
		}
		content = strings.NewReplacer("{{role}}", role, "{{exec}}", strings.Join(quoted, " "), "{{path}}", path).Replace(systemdTemplate)
		file = filepath.Join(home, ".config", "systemd", "user", "yip-"+role+".service")
		activate = "systemctl --user daemon-reload\nsystemctl --user enable --now yip-" + role + "\nloginctl enable-linger $USER   # keep running without an active login"
	default:
		return fmt.Errorf("service installation isn't supported on %s", runtime.GOOS)
	}
	if *printOnly {
		fmt.Print(content)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		return err
	}
	fmt.Printf("Wrote %s\nActivate it with:\n%s\n\nThen verify restart after a process failure and after a reboot under this account (yip doctor).\n", file, activate)
	if runtime.GOOS == "darwin" {
		fmt.Println("Note: a user LaunchAgent starts at login. After a reboot with FileVault, the machine must be unlocked and this user logged in before work resumes.")
	}
	return nil
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func diskFreeBytes(path string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0
	}
	return int64(st.Bavail) * int64(st.Bsize)
}
