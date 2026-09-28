package bridge

import "testing"

func TestEveryToolSaysWhatItIsDoing(t *testing.T) {
	for _, tool := range Tools {
		if Activity(tool.Name) == "" {
			t.Errorf("%s has no activity phrase for the room", tool.Name)
		}
	}
}
