package cursor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImportedConfigStillGatesPermissionsAndHooks(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".cursor"), 0700); err != nil {
		t.Fatal(err)
	}
	a := &Adapter{approvedConfig: map[string]bool{}}
	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, ".cursor", name), []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("mcp.json")
	if _, err := a.checkImportedProjectConfig(dir, false); err == nil {
		t.Fatal("native MCP gate lost")
	}
	if _, err := a.checkImportedProjectConfig(dir, true); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hooks.json", "cli.json"} {
		write(name)
		if _, err := a.checkImportedProjectConfig(dir, true); err == nil {
			t.Fatalf("import relaxed %s", name)
		}
		if err := os.Remove(filepath.Join(dir, ".cursor", name)); err != nil {
			t.Fatal(err)
		}
	}
}
