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
		args, want []string
	}{
		{name: "default", want: []string{"up", "--build"}},
		{name: "detached", args: []string{"up", "--build", "-d"}, want: []string{"up", "--build", "-d"}},
		{name: "empty", args: []string{"exec", "hub", "printf", "%s", ""}, want: []string{"exec", "hub", "printf", "%s", ""}},
		{name: "quoted", args: []string{"exec", "hub", "printf", "%s", "two words; $(exit 9)"}, want: []string{"exec", "hub", "printf", "%s", "two words; $(exit 9)"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			record := filepath.Join(dir, "args")
			if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\nprintf '%s\\0' \"$@\" > \"$YIP_TEST_DOCKER_ARGS\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, just, append([]string{"docker"}, tc.args...)...)
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
		})
	}
}
