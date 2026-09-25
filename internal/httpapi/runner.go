package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"

	"github.com/binbandit/yip/internal/auth"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/protocol"
)

// RunnerServer is the runner-facing listener. It speaks TLS with a
// certificate from the hub CA; pairing needs an enrollment token, and every
// other endpoint requires a verified client certificate.
type RunnerServer struct {
	hub *hub.Hub
	mux *http.ServeMux
	log func(msg string, args ...any)
}

func NewRunnerServer(h *hub.Hub, logf func(msg string, args ...any)) *RunnerServer {
	s := &RunnerServer{hub: h, mux: http.NewServeMux(), log: logf}
	s.mux.HandleFunc("POST /v1/nodes/pair", s.pair)
	s.mux.HandleFunc("GET /v1/runner/connect", s.node(s.connect))
	// The websocket checks the credential in ConnectRunner (and tells a
	// revoked runner to stop); plain requests check it here.
	s.mux.HandleFunc("PUT /v1/runner/artifacts/{hash}", s.node(s.current(s.putArtifact)))
	s.mux.HandleFunc("GET /v1/runner/artifacts/{id}", s.node(s.current(s.getArtifact)))
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	return s
}

func (s *RunnerServer) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

type nodeKey struct{}

type nodeIdentity struct{ id, serial string }

func (s *RunnerServer) node(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
			writeJSON(w, 401, domain.Unauthorized("A paired machine certificate is required.").API(""))
			return
		}
		id, serial := auth.NodeIDFromCert(r.TLS.PeerCertificates[0])
		if id == "" {
			writeJSON(w, 401, domain.Unauthorized("Certificate is not a yip node identity.").API(""))
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), nodeKey{}, nodeIdentity{id, serial})))
	}
}

// current refuses a revoked or superseded machine credential.
func (s *RunnerServer) current(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident := r.Context().Value(nodeKey{}).(nodeIdentity)
		if err := s.hub.AuthorizeNode(r.Context(), ident.id, ident.serial); err != nil {
			de := domain.AsError(err)
			writeJSON(w, de.Status, de.API(""))
			return
		}
		h(w, r)
	}
}

func (s *RunnerServer) pair(w http.ResponseWriter, r *http.Request) {
	var req protocol.PairRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeJSON(w, 400, domain.Invalid("Invalid pairing request.").API(""))
		return
	}
	resp, err := s.hub.Pair(r.Context(), req)
	if err != nil {
		de := domain.AsError(err)
		writeJSON(w, de.Status, de.API(""))
		return
	}
	writeJSON(w, 201, resp)
}

func (s *RunnerServer) connect(w http.ResponseWriter, r *http.Request) {
	ident := r.Context().Value(nodeKey{}).(nodeIdentity)
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	c.SetReadLimit(32 << 20)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readFrame := func() (protocol.Frame, error) {
		_, data, err := c.Read(ctx)
		if err != nil {
			return protocol.Frame{}, err
		}
		var f protocol.Frame
		return f, json.Unmarshal(data, &f)
	}
	helloCtx, helloCancel := context.WithTimeout(ctx, 15*time.Second)
	_, data, err := c.Read(helloCtx)
	helloCancel()
	if err != nil {
		c.Close(websocket.StatusPolicyViolation, "expected hello")
		return
	}
	var first protocol.Frame
	var hello protocol.Hello
	if json.Unmarshal(data, &first) != nil || first.Type != protocol.EvHello || json.Unmarshal(first.Payload, &hello) != nil {
		c.Close(websocket.StatusPolicyViolation, "expected hello")
		return
	}
	conn := &hub.NodeConn{NodeID: ident.id, Serial: ident.serial, ConnectedAt: time.Now(),
		Send: func(f protocol.Frame) error {
			b, err := json.Marshal(f)
			if err != nil {
				return err
			}
			wctx, wcancel := context.WithTimeout(ctx, 20*time.Second)
			defer wcancel()
			return c.Write(wctx, websocket.MessageText, b)
		},
		CloseFn: func(reason string) {
			c.Close(websocket.StatusPolicyViolation, reason)
			cancel()
		},
	}
	if err := s.hub.ConnectRunner(ctx, conn, hello); err != nil {
		de := domain.AsError(err)
		reason := de.Message
		if len(reason) > 120 {
			reason = reason[:120]
		}
		c.Close(websocket.StatusPolicyViolation, reason)
		return
	}
	defer s.hub.DisconnectRunner(context.Background(), conn)
	if s.log != nil {
		s.log("runner connected", "node", ident.id)
	}
	for {
		f, err := readFrame()
		if err != nil {
			if s.log != nil {
				s.log("runner disconnected", "node", ident.id, "err", err)
			}
			return
		}
		// Frames from one runner are processed in order.
		s.hub.RunnerFrame(ctx, conn, f)
	}
}

func (s *RunnerServer) putArtifact(w http.ResponseWriter, r *http.Request) {
	ident := r.Context().Value(nodeKey{}).(nodeIdentity)
	if !s.hub.NodeHoldsRun(r.Context(), ident.id) {
		writeJSON(w, 403, domain.Forbidden("Uploads are accepted only for a run this machine currently holds.").API(""))
		return
	}
	size, err := strconv.ParseInt(r.Header.Get("X-Yip-Size"), 10, 64)
	if err != nil {
		writeJSON(w, 400, domain.Invalid("X-Yip-Size is required.").API(""))
		return
	}
	body := http.MaxBytesReader(w, r.Body, hub.MaxArtifactBytes+1)
	if err := s.hub.Artifacts().Put(body, r.PathValue("hash"), size); err != nil {
		writeJSON(w, 422, domain.Invalid("%s", err.Error()).API(""))
		return
	}
	w.WriteHeader(204)
}

func (s *RunnerServer) getArtifact(w http.ResponseWriter, r *http.Request) {
	ident := r.Context().Value(nodeKey{}).(nodeIdentity)
	a, f, err := s.hub.OpenArtifactForNode(r.Context(), ident.id, r.PathValue("id"))
	if err != nil {
		de := domain.AsError(err)
		writeJSON(w, de.Status, de.API(""))
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Artifact-Sha256", a.Hash)
	http.ServeContent(w, r, "", a.CreatedAt, f)
}
