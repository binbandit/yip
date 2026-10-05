package skills

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestPinnedUpstreamFiles(t *testing.T) {
	// Exact bytes from the selected upstream revision, including its license.
	want := map[string]string{
		"LICENSE":                    "76c67452ce8d25e9395a019f16a6f326c615782f6085ceae59ce7419b015228c",
		"skills/arena/SKILL.md":      "4ad33a3745109bb8350a0c73af077ae2e4e386061785e26cd395543ee475f8e1",
		"skills/babysit-pr/SKILL.md": "41a251cee38434d040158867e8155bd62bbb2616e8fcd7f90b916397df6de9cb",
		"skills/bro/SKILL.md":        "563c4b174615de84dc352119f70cb1a4cfb72c8a08fe1368a19ccb22f3a2dabb",
		"skills/file-pr/SKILL.md":    "2f686252308ee718044ecfc0f983413e27196d8b7e9961f7adfa55762691d850",
	}
	for path, hash := range want {
		b, err := files.ReadFile("bin-stack/" + path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(b)); got != hash {
			t.Errorf("%s differs from upstream %s: %s", path, Revision, got)
		}
	}
	if len(Catalog()) != 4 {
		t.Fatal("incomplete catalog")
	}
	for _, item := range Catalog() {
		doc, err := Read(item.Name)
		if err != nil || item.Description == "" || !strings.Contains(doc.Content, "name: "+item.Name+"\n") || doc.Revision != Revision || !strings.Contains(doc.Source, Revision) || len(doc.SHA256) != 64 {
			t.Fatalf("invalid pinned skill: %+v, %v", doc, err)
		}
	}
}

func TestOnlyCatalogNamesCanBeRead(t *testing.T) {
	for _, name := range []string{"", "../LICENSE", "/etc/passwd", "bro/SKILL.md", "BRO", "bro\x00", "https://example.com/skill"} {
		if _, err := Read(name); err == nil {
			t.Errorf("accepted noncatalog name %q", name)
		}
	}
	list := Catalog()
	list[0].Name = "changed"
	if Catalog()[0].Name == "changed" {
		t.Fatal("catalog leaked mutable state")
	}
}
