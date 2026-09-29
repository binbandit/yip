package main

import (
	"flag"
	"strings"
	"testing"
)

func TestDefaultAdaptersIncludeHarnesses(t *testing.T) {
	got, err := adapters(defaultProviders)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range strings.Split(defaultProviders, ",") {
		if got[id] == nil || got[id].Name() != id {
			t.Errorf("adapter %q missing or registered under the wrong name", id)
		}
	}
	for _, id := range []string{"opencode", "pi"} {
		if got[id] == nil {
			t.Fatalf("default runner does not enable %s", id)
		}
	}
	var flags hubFlags
	flags.register(flag.NewFlagSet("test", flag.ContinueOnError), t.TempDir())
	if flags.localProviders != defaultProviders {
		t.Fatalf("local runner defaults differ: %s", flags.localProviders)
	}
}

func TestExplicitHarnessAdapters(t *testing.T) {
	got, err := adapters(" opencode, pi ")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["opencode"].Label() != "OpenCode" || got["pi"].Label() != "Pi Agent Harness" {
		t.Fatalf("unexpected adapters: %#v", got)
	}
	if _, err := adapters("unknown"); err == nil {
		t.Fatal("unknown adapter accepted")
	}
}

func TestAgentWorkerSupportsEveryDefaultProvider(t *testing.T) {
	for _, name := range strings.Split(defaultProviders, ",") {
		adapter, err := agentWorkerAdapter(name, []string{"HOME=/home/yip"})
		if err != nil {
			t.Fatalf("container worker cannot construct %s: %v", name, err)
		}
		if adapter.Name() != name {
			t.Errorf("worker provider %s returned %s", name, adapter.Name())
		}
	}
	if _, err := agentWorkerAdapter("unknown", nil); err == nil {
		t.Fatal("unknown worker provider accepted")
	}
}
