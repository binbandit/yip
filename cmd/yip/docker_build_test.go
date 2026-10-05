package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Exercise the real build script with fake external commands. The fake Go
// compiler uses the real `go env` so exported and persisted settings are tested.
func TestLocalDockerBuild(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	const proxy = "https://test-user:synthetic-secret@example.invalid/go?token=test-only"
	for _, tc := range []struct {
		name, daemon, platform, arch, proxy, storedProxy, fail string
	}{
		{name: "daemon amd64 from Mac", daemon: "linux/x86_64", arch: "amd64", proxy: proxy},
		{name: "daemon arm64", daemon: "linux/aarch64", arch: "arm64", storedProxy: proxy},
		{name: "explicit platform", daemon: "linux/aarch64", platform: "linux/amd64", arch: "amd64", proxy: proxy, storedProxy: "https://ignored.invalid"},
		{name: "unset defaults", daemon: "linux/amd64", arch: "amd64"},
		{name: "Go failure stops packaging", daemon: "linux/amd64", fail: "go"},
		{name: "web failure stops compilation", daemon: "linux/amd64", fail: "npm"},
		{name: "unsupported OS", daemon: "windows/x86_64", fail: "platform"},
		{name: "unsupported architecture", daemon: "linux/s390x", fail: "platform"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name, contents string) {
				t.Helper()
				path := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(contents), 0700); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{"scripts/docker-build.sh", "packaging/container/Dockerfile.hub-local"} {
				data, err := os.ReadFile(filepath.Join(root, name))
				if err != nil {
					t.Fatal(err)
				}
				write(name, string(data))
			}
			write("web/package.json", "{}")
			stored := ""
			if tc.storedProxy != "" {
				stored = "GOPROXY=" + tc.storedProxy + "\n"
			}
			write("go-settings", stored)
			write("tools/npm", `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$YIP_TEST_ROOT/npm-calls"
[ "$YIP_TEST_FAIL" != npm ]
`)
			write("tools/go", `#!/bin/sh
set -eu
"$YIP_TEST_REAL_GO" env -json GOOS GOARCH CGO_ENABLED GOPROXY GOPRIVATE GONOPROXY GONOSUMDB GOSUMDB > "$YIP_TEST_ROOT/go-env.json"
[ "$YIP_TEST_FAIL" != go ]
while [ "$#" -gt 0 ]; do
    if [ "$1" = -o ]; then shift; printf 'synthetic Linux binary' > "$1"; chmod 700 "$1"; exit 0; fi
    shift
done
exit 1
`)
			write("tools/docker", `#!/bin/sh
set -eu
if [ "$1" = info ]; then printf '%s\n' "$YIP_TEST_DAEMON"; exit 0; fi
[ "$1" = build ]
printf '%s\n' "$@" > "$YIP_TEST_ROOT/docker-args"
for context do :; done
printf '%s' "$context" > "$YIP_TEST_ROOT/context-path"
cp -R "$context" "$YIP_TEST_ROOT/sent-context"
`)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "sh", "scripts/docker-build.sh", "test-local")
			cmd.Dir = dir
			for _, env := range os.Environ() {
				name, _, _ := strings.Cut(env, "=")
				switch name {
				case "GOPROXY", "GOPRIVATE", "GONOPROXY", "GONOSUMDB", "GOSUMDB", "GOENV", "GOOS", "GOARCH", "CGO_ENABLED", "GOWORK", "GOTOOLCHAIN", "DOCKER_DEFAULT_PLATFORM":
					continue
				}
				cmd.Env = append(cmd.Env, env)
			}
			cmd.Env = append(cmd.Env, "PATH="+filepath.Join(dir, "tools")+string(os.PathListSeparator)+os.Getenv("PATH"),
				"YIP_TEST_ROOT="+dir, "YIP_TEST_FAIL="+tc.fail, "YIP_TEST_DAEMON="+tc.daemon,
				"YIP_TEST_REAL_GO="+filepath.Join(runtime.GOROOT(), "bin", "go"), "GOENV="+filepath.Join(dir, "go-settings"),
				"GOWORK=off", "GOTOOLCHAIN=local", "GOOS=darwin", "GOARCH=arm64", "CGO_ENABLED=1",
				"DOCKER_DEFAULT_PLATFORM="+tc.platform)
			if tc.proxy != "" {
				cmd.Env = append(cmd.Env, "GOPROXY="+tc.proxy, "GOPRIVATE=*.example.invalid", "GONOPROXY=none", "GONOSUMDB=private.example.invalid", "GOSUMDB=sum.golang.org")
			}
			output, err := cmd.CombinedOutput()
			if tc.fail != "" {
				if err == nil {
					t.Fatalf("expected failure, got success: %s", output)
				}
				if _, err := os.Stat(filepath.Join(dir, "sent-context")); !os.IsNotExist(err) {
					t.Fatal("failed compilation must not package or replace the previous image")
				}
				if tc.fail != "go" {
					if _, err := os.Stat(filepath.Join(dir, "go-env.json")); !os.IsNotExist(err) {
						t.Fatal("must stop before compilation")
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("build failed: %v\n%s", err, output)
			}
			var settings map[string]string
			data, err := os.ReadFile(filepath.Join(dir, "go-env.json"))
			if err != nil || json.Unmarshal(data, &settings) != nil {
				t.Fatalf("read Go settings: %s, %v", data, err)
			}
			wantProxy := tc.proxy
			if wantProxy == "" {
				wantProxy = tc.storedProxy
			}
			if wantProxy == "" {
				wantProxy = "https://proxy.golang.org,direct"
			}
			if settings["GOOS"] != "linux" || settings["GOARCH"] != tc.arch || settings["CGO_ENABLED"] != "0" || settings["GOPROXY"] != wantProxy {
				t.Fatalf("unexpected build environment: %v", settings)
			}
			if tc.proxy != "" && (settings["GOPRIVATE"] != "*.example.invalid" || settings["GONOPROXY"] != "none" || settings["GONOSUMDB"] != "private.example.invalid" || settings["GOSUMDB"] != "sum.golang.org") {
				t.Fatalf("module settings changed: %v", settings)
			}
			args, _ := os.ReadFile(filepath.Join(dir, "docker-args"))
			if !strings.HasPrefix(string(args), "build\n--platform\nlinux/"+tc.arch+"\n--tag\nyip-hub:local\n") {
				t.Fatalf("image platform does not match binary: %s", args)
			}
			files, err := os.ReadDir(filepath.Join(dir, "sent-context"))
			if err != nil || len(files) != 2 || files[0].Name() != "Dockerfile" || files[1].Name() != "yip" {
				t.Fatalf("build context must contain only runtime Dockerfile and binary: %v, %v", files, err)
			}
			binary, err := os.Stat(filepath.Join(dir, "sent-context", "yip"))
			if err != nil || binary.Mode().Perm() != 0755 {
				t.Fatalf("binary must be readable and executable by the container's non-root user: %v, %v", binary, err)
			}
			contextPath, _ := os.ReadFile(filepath.Join(dir, "context-path"))
			if _, err := os.Stat(string(contextPath)); !os.IsNotExist(err) {
				t.Fatal("temporary build context was not removed")
			}
			if strings.Contains(string(output), "synthetic-secret") || strings.Contains(string(args), "synthetic-secret") {
				t.Fatal("proxy credential appeared in build command or output")
			}
		})
	}
}
