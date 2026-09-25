package bridge

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/binbandit/yip/protocol"
)

// TestMain lets the test binary act as `yip bridge` for the round-trip test.
func TestMain(m *testing.M) {
	if os.Getenv("YIP_TEST_ACT_AS_BRIDGE") == "1" {
		if err := RunFromEnv(os.Getenv("YIP_TEST_MODE"), "test"); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestBridgeRoundTripBindsTokenAndMode(t *testing.T) {
	dir, err := os.MkdirTemp("", "yipb")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "b.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var seenToken string
	go Serve(ln, func(req LocalRequest) LocalResponse {
		seenToken = req.Token
		if req.Tool == RoomPost {
			var a RoomPostArgs
			_ = json.Unmarshal(req.Args, &a)
			res, _ := json.Marshal(map[string]string{"messageId": "m-1", "echo": a.Body})
			return LocalResponse{OK: true, Result: res}
		}
		return LocalResponse{Error: &protocol.APIError{Code: "forbidden", Message: "nope"}}
	})

	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "YIP_TEST_ACT_AS_BRIDGE=1", "YIP_TEST_MODE=readonly", EnvSocket+"="+sock, EnvToken+"=run-secret")
	cmd.Stderr = io.Discard
	c, err := StartMCP(cmd)
	if err != nil {
		t.Fatal(err)
	}
	names, err := c.ListTools()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, WorkReview) || strings.Contains(joined, WorkPublishRev) || strings.Contains(joined, WorkCreate) {
		t.Fatalf("readonly tools wrong: %s", joined)
	}
	text, isErr, err := c.CallTool(RoomPost, map[string]any{"body": "hello"})
	if err != nil || isErr || !strings.Contains(text, "hello") {
		t.Fatalf("room_post: %q %v %v", text, isErr, err)
	}
	if seenToken != "run-secret" {
		t.Fatalf("token not forwarded: %q", seenToken)
	}
	// Not advertised in readonly mode → refused by the bridge itself.
	text, isErr, _ = c.CallTool(WorkPublishRev, map[string]any{"summary": "x"})
	if !isErr || !strings.Contains(text, "not available") {
		t.Fatalf("expected refusal, got %q", text)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("bridge exit: %v", err)
	}
}
