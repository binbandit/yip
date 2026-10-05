package install

import (
	"os"
	"reflect"
	"testing"
)

func TestPlistRoundTripPreservesOtherValues(t *testing.T) {
	f := newFixture(t)
	_, data := f.service(t, "runner", false)
	before, err := parsePlist(data)
	if err != nil {
		t.Fatal(err)
	}
	updated, disabled, err := updatePlist(data, "dev.getyip.runner", "/new & special/yip", f.i.verify)
	if err != nil || disabled {
		t.Fatalf("update: %v, disabled %v", err, disabled)
	}
	after, err := parsePlist(updated)
	if err != nil {
		t.Fatal(err)
	}
	before.Nodes[0].get("Program").Text = "/new & special/yip"
	before.Nodes[0].get("ProgramArguments").Nodes[0].Text = "/new & special/yip"
	clearContainerText(before)
	clearContainerText(after)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("settings changed unexpectedly:\n%s", updated)
	}
}

func TestPlistRejectsAmbiguousInput(t *testing.T) {
	for _, input := range []string{
		`<plist><dict/></plist><plist><dict/></plist>`,
		`<plist><dict><key>a</key></dict></plist>`,
		`<plist><dict><string>a</string><string>b</string></dict></plist>`,
		`<plist><dict><key>a</key><dict><key>b</key><true/><key>b</key><false/></dict></dict></plist>`,
		`<plist><dict><key>a</key><true>not a bool</true></dict></plist>`,
		`<plist><dict><key>a</key><string><key>b</key></string></dict></plist>`,
		`<plist xmlns="foreign"><dict/></plist>`,
		`bplist00`,
	} {
		if _, err := parsePlist([]byte(input)); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
}

func TestSelfReplacement(t *testing.T) {
	f := newFixture(t)
	f.source = f.old
	writeFixture(t, f.old, []byte("yip-new"), 0o755)
	if err := f.i.run(f.source, ""); err != nil {
		t.Fatal(err)
	}
	requireFile(t, f.old, []byte("yip-new"))
	entries, err := os.ReadDir(f.i.home + "/existing bin")
	if err != nil || len(entries) != 1 {
		t.Fatalf("staging leftovers: %v, %v", entries, err)
	}
}
