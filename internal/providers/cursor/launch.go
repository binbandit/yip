package cursor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/internal/providers/acp"
)

// cursorAuthMethod is the ACP auth method Cursor documents
// (https://cursor.com/docs/cli/acp#authentication). It validates an existing
// CLI login (`agent login`) or CURSOR_API_KEY / CURSOR_AUTH_TOKEN.
const cursorAuthMethod = "cursor_login"

// agentProc is one running `agent acp` process and its ACP connection.
type agentProc struct {
	proc   *providers.Process
	conn   *acp.Conn
	stdin  io.WriteCloser
	stdout *os.File
	stderr *tailBuffer
	served chan struct{}

	stopOnce sync.Once
	stopOK   bool
}

// launch starts `<exe> acp` in its own process group with the given
// allowlisted environment. No bypass/force flags are ever passed.
func launch(exe, dir string, env []string, h acp.Handler) (*agentProc, error) {
	cmd := exec.Command(exe, "acp")
	cmd.Dir = dir
	cmd.Env = launchEnv(env)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = pw
	tail := newTailBuffer(16 << 10)
	cmd.Stderr = tail
	// Bound how long Wait blocks on stderr held open by stray grandchildren.
	cmd.WaitDelay = 3 * time.Second
	proc, err := providers.StartProcess(cmd)
	_ = pw.Close()
	if err != nil {
		_ = pr.Close()
		return nil, err
	}
	ap := &agentProc{proc: proc, stdin: stdin, stdout: pr, stderr: tail, served: make(chan struct{})}
	ap.conn = acp.NewConn(pr, stdin, h)
	go func() {
		_ = ap.conn.Serve()
		close(ap.served)
	}()
	return ap, nil
}

// stop closes stdin (a clean shutdown request for a stdio server), gives the
// agent eofWait to exit on its own (so it can persist session state for a
// later session/load), then terminates the whole process group. It
// returns true only when the group is confirmed gone. Idempotent.
func (ap *agentProc) stop(eofWait, grace time.Duration) bool {
	ap.stopOnce.Do(func() {
		_ = ap.stdin.Close()
		select {
		case <-ap.proc.Done():
		case <-time.After(eofWait):
		}
		ap.stopOK = ap.proc.Terminate(grace)
		select {
		case <-ap.served:
		case <-time.After(2 * time.Second):
			// A process outside the group still holds stdout; stop reading.
			_ = ap.stdout.Close()
			<-ap.served
		}
	})
	return ap.stopOK
}

// exitSummary describes how the process ended, with the stderr tail.
func (ap *agentProc) exitSummary() string {
	var b strings.Builder
	select {
	case <-ap.proc.Done():
		fmt.Fprintf(&b, "Cursor agent exited (code %d)", providers.ExitCode(ap.proc.Err()))
	default:
		b.WriteString("Cursor agent closed its ACP stream")
	}
	if t := strings.TrimSpace(ap.stderr.String()); t != "" {
		b.WriteString(": ")
		b.WriteString(lastLines(t, 12, 2000))
	}
	return b.String()
}

// waitExit waits briefly for the process to exit so the exit code and
// complete stderr are available.
func (ap *agentProc) waitExit(d time.Duration) {
	select {
	case <-ap.proc.Done():
	case <-time.After(d):
	}
}

// initialize performs the ACP handshake with honest client capabilities: no
// fs/* and no terminal/* methods are offered, so the agent uses its own
// tools, which Cursor gates with session/request_permission.
func (ap *agentProc) initialize(ctx context.Context) (acp.InitializeResult, error) {
	var res acp.InitializeResult
	err := ap.conn.Call(ctx, acp.MethodInitialize, acp.InitializeParams{
		ProtocolVersion:    acp.ProtocolVersion,
		ClientCapabilities: acp.ClientCapabilities{FS: acp.FileSystemCapability{ReadTextFile: false, WriteTextFile: false}, Terminal: false},
		ClientInfo:         &acp.Implementation{Name: "yip", Title: "yip runner", Version: "1"},
	}, &res)
	if err != nil {
		return res, err
	}
	if res.ProtocolVersion != acp.ProtocolVersion {
		return res, fmt.Errorf("%w: Cursor agent speaks ACP protocol version %d; yip supports version %d", providers.ErrUnsupported, res.ProtocolVersion, acp.ProtocolVersion)
	}
	return res, nil
}

// hasAuthMethod reports whether the agent advertised the given method.
func hasAuthMethod(init acp.InitializeResult, id string) bool {
	for _, m := range init.AuthMethods {
		if m.ID == id && (m.Type == "" || m.Type == "agent") {
			return true
		}
	}
	return false
}

// tailBuffer keeps the last max bytes written (stderr tail for diagnostics).
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func newTailBuffer(max int) *tailBuffer { return &tailBuffer{max: max} }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append([]byte(nil), t.buf[over:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func lastLines(s string, n, maxBytes int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := strings.Join(lines, "\n")
	if len(out) > maxBytes {
		out = "…" + out[len(out)-maxBytes:]
	}
	return out
}

// runCmd runs a short-lived CLI subcommand in its own process group with the
// allowlisted environment, killing the group on timeout.
func runCmd(ctx context.Context, exe string, env []string, timeout time.Duration, args ...string) (stdout, stderr string, err error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = launchEnv(env)
	cmd.Dir = os.TempDir()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err = cmd.Run()
	if ctx.Err() != nil && err != nil {
		err = fmt.Errorf("%s %s timed out: %w", exe, strings.Join(args, " "), ctx.Err())
	}
	return o.String(), e.String(), err
}

// rpcError extracts a JSON-RPC error.
func rpcError(err error) (*acp.Error, bool) {
	var e *acp.Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
