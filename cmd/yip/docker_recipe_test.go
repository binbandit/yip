package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDockerRecipePreservesArguments(t *testing.T) {
	just, err := exec.LookPath("just")
	if err != nil {
		t.Skip("just is required to exercise the recipe")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		recipe     string
		args, want []string
	}{
		{name: "default", want: []string{"up", "--build"}},
		{name: "detached", args: []string{"up", "--build", "-d"}, want: []string{"up", "--build", "-d"}},
		{name: "empty", args: []string{"exec", "hub", "printf", "%s", ""}, want: []string{"exec", "hub", "printf", "%s", ""}},
		{name: "quoted", args: []string{"exec", "hub", "printf", "%s", "two words; $(exit 9)"}, want: []string{"exec", "hub", "printf", "%s", "two words; $(exit 9)"}},
		{name: "reuse", recipe: "docker-run", want: []string{"up", "--no-build", "--pull", "never"}},
		{name: "reuse detached", recipe: "docker-run", args: []string{"-d"}, want: []string{"up", "--no-build", "--pull", "never", "-d"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			record := filepath.Join(dir, "args")
			if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\nprintf '%s\\0' \"$@\" > \"$YIP_TEST_DOCKER_ARGS\"\nprintf '%s' \"${YIP_HUB_IMAGE:-}\" > \"$YIP_TEST_DOCKER_ARGS.image\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			for _, tool := range []string{"go", "npm"} {
				if err := os.WriteFile(filepath.Join(dir, tool), []byte("#!/bin/sh\necho 'run must not compile' >&2\nexit 99\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			recipe := tc.recipe
			if recipe == "" {
				recipe = "docker"
			}
			cmd := exec.CommandContext(ctx, just, append([]string{recipe}, tc.args...)...)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "YIP_TEST_DOCKER_ARGS="+record)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("recipe: %v\n%s", err, output)
			}
			raw, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
			want := append([]string{"compose", "-f", "packaging/container/compose.hub.yml"}, tc.want...)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("arguments changed: got %q, want %q", got, want)
			}
			if tc.recipe == "docker-run" {
				image, err := os.ReadFile(record + ".image")
				if err != nil || string(image) != "yip-hub:local" {
					t.Fatalf("reuse must select the local image: %q, %v", image, err)
				}
			}
		})
	}
}
