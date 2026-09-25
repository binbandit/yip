// Package hub is yip's canonical state machine and coordinator: rooms and
// messages, recipient routing, jobs and runs, reviews, questions, approvals,
// scheduling, runner connections, and the agent tool surface.
//
// Every state change goes through a validated hub operation. Each mutation,
// its event, and any dispatch outbox item commit in one transaction; the
// dispatcher only sends committed outbox rows.
package hub

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/binbandit/yip/internal/auth"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/events"
	"github.com/binbandit/yip/internal/forge"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

// Config configures a hub.
type Config struct {
	DataDir string
	Version string
	Limits  domain.Limits
	Logger  *slog.Logger
	// RunnerURL is the address runners use to reach the hub's runner
	// listener (https://host:port). Shown in enrollment commands.
	RunnerURL string
	// Demo marks a hub seeded with fixture data and the fake provider.
	Demo bool
	// ForgeFactory builds a forge connector for a repository.
	ForgeFactory func(ctx context.Context, h *Hub, repo protocol.Repo) (forge.Connector, forge.RepoRef, error)
	// Now overrides the clock in tests.
	Now func() time.Time
}

// Hub is the running coordinator.
type Hub struct {
	cfg       Config
	st        *store.Store
	bus       *events.Bus
	lim       domain.Limits
	log       *slog.Logger
	ca        *auth.CA
	sealer    *auth.Sealer
	artifacts *ArtifactStore
	logins    *auth.Limiter

	orgMu sync.RWMutex
	org   protocol.Org

	nodes *nodeRegistry
	kick  chan struct{}

	// toolWaiters holds approval requests blocked on a human decision.
	mu sync.Mutex
	// excluded temporarily keeps a node out of scheduling for a run after
	// the runner rejected an offer.
	excluded map[string]time.Time
	closed   chan struct{}
	wg       sync.WaitGroup
}

// Open opens the hub's data directory: database, CA, hub key, artifacts.
func Open(ctx context.Context, cfg Config) (*Hub, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	if cfg.Limits.LeaseDuration == 0 {
		cfg.Limits = domain.DefaultLimits()
	}
	if cfg.Now == nil {
		cfg.Now = domain.Now
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	st, err := store.Open(ctx, filepath.Join(cfg.DataDir, "hub.db"))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	ca, err := auth.LoadOrCreateCA(filepath.Join(cfg.DataDir, "pki"))
	if err != nil {
		st.Close()
		return nil, fmt.Errorf("hub CA: %w", err)
	}
	sealer, err := auth.LoadOrCreateKey(cfg.DataDir)
	if err != nil {
		st.Close()
		return nil, fmt.Errorf("hub key: %w", err)
	}
	arts, err := NewArtifactStore(filepath.Join(cfg.DataDir, "artifacts"))
	if err != nil {
		st.Close()
		return nil, err
	}
	h := &Hub{
		cfg: cfg, st: st, bus: events.NewBus(), lim: cfg.Limits, log: cfg.Logger, ca: ca, sealer: sealer,
		artifacts: arts, logins: auth.NewLimiter(8, 10*time.Minute), nodes: newNodeRegistry(),
		kick: make(chan struct{}, 1), excluded: map[string]time.Time{}, closed: make(chan struct{}),
	}
	if org, err := store.FirstOrg(ctx, st.R()); err == nil {
		h.org = org
	} else if !errors.Is(err, store.ErrNotFound) {
		st.Close()
		return nil, err
	}
	return h, nil
}

// Store exposes the database (for CLI tools and tests).
func (h *Hub) Store() *store.Store { return h.st }

// Bus exposes the event bus.
func (h *Hub) Bus() *events.Bus { return h.bus }

// CA exposes the hub certificate authority.
func (h *Hub) CA() *auth.CA { return h.ca }

// Limits returns the operating limits.
func (h *Hub) Limits() domain.Limits { return h.lim }

// Config returns the hub configuration.
func (h *Hub) Config() Config { return h.cfg }

// Artifacts exposes the artifact store.
func (h *Hub) Artifacts() *ArtifactStore { return h.artifacts }

func (h *Hub) now() time.Time { return h.cfg.Now().UTC().Truncate(time.Millisecond) }

// Org returns the organisation (zero before setup).
func (h *Hub) Org() protocol.Org {
	h.orgMu.RLock()
	defer h.orgMu.RUnlock()
	return h.org
}

func (h *Hub) setOrg(o protocol.Org) {
	h.orgMu.Lock()
	h.org = o
	h.orgMu.Unlock()
}

// Start launches background loops (scheduler, lease supervision, outbox
// dispatch). It returns immediately.
func (h *Hub) Start(ctx context.Context) {
	h.recoverOnStart(ctx)
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		h.loop(ctx)
	}()
}

// Close stops loops and closes the database.
func (h *Hub) Close() error {
	select {
	case <-h.closed:
	default:
		close(h.closed)
	}
	h.wg.Wait()
	h.nodes.closeAll("hub shutting down")
	return h.st.Close()
}

// Kick asks the scheduler to run soon.
func (h *Hub) Kick() {
	select {
	case h.kick <- struct{}{}:
	default:
	}
}

// ---- transactions ----

// txn is one hub mutation: its rows, events, and outbox items commit together.
type txn struct {
	h      *Hub
	ctx    context.Context
	tx     *sql.Tx
	emits  int
	after  []func()
	kicked bool
}

func (h *Hub) do(ctx context.Context, fn func(t *txn) error) error {
	var t *txn
	err := h.st.Tx(ctx, func(tx *sql.Tx) error {
		t = &txn{h: h, ctx: ctx, tx: tx}
		return fn(t)
	})
	if err != nil {
		return err
	}
	if t.emits > 0 {
		h.bus.Notify()
	}
	for _, f := range t.after {
		f()
	}
	if t.kicked {
		h.Kick()
	}
	return nil
}

// afterCommit schedules f to run only if the transaction commits.
func (t *txn) afterCommit(f func()) { t.after = append(t.after, f) }

// kickAfter wakes the scheduler after commit.
func (t *txn) kickAfter() { t.kicked = true }

// ev describes an event to append.
type ev struct {
	Type        string
	Actor       protocol.Actor
	Room        string
	Thread      string
	Job         string
	Run         string
	Cause       string
	Root        string
	Payload     any
	Visibility  string // default: room:<Room> when Room is set, else owner
	ProducerKey string
}

var systemActor = protocol.Actor{Kind: protocol.ActorSystem, ID: "hub"}

// emit appends an event in the transaction.
func (t *txn) emit(e ev) error {
	vis := e.Visibility
	if vis == "" {
		if e.Room != "" {
			vis = "room:" + e.Room
		} else {
			vis = "owner"
		}
	}
	if e.Actor.Kind == "" {
		e.Actor = systemActor
	}
	var payload json.RawMessage
	if e.Payload != nil {
		b, err := json.Marshal(e.Payload)
		if err != nil {
			return err
		}
		payload = b
	}
	_, _, err := store.InsertEvent(t.ctx, t.tx, store.EventRow{
		Event: protocol.Event{
			SchemaVersion: protocol.SchemaVersion, EventID: domain.NewID(), OrgID: t.h.Org().ID, Type: e.Type, Actor: e.Actor,
			CauseID: e.Cause, RootRequestID: e.Root, RoomID: e.Room, ThreadID: e.Thread, JobID: e.Job, RunID: e.Run,
			OccurredAt: t.h.now(), Payload: payload,
		},
		Visibility: vis, ProducerKey: e.ProducerKey,
	})
	if err == nil {
		t.emits++
	}
	return err
}

func (t *txn) audit(actor protocol.Actor, authority, action, target, result, detail string) error {
	return store.InsertAudit(t.ctx, t.tx, domain.NewID(), store.Audit{
		OrgID: t.h.Org().ID, Actor: actor, Authority: authority, Action: action, Target: target, Result: result, Detail: detail,
	})
}

// ---- messages ----

type newMessage struct {
	Room       string
	Thread     string
	Author     protocol.Actor
	Body       string
	Kind       string
	Mentions   []protocol.Mention
	Refs       []protocol.Ref
	ProjectIDs []string
	ReplyTo    string
	RunID      string
	JobID      string
	ClientKey  string
	Cause      string
	Root       string
}

// postMessage persists a message in the transaction and emits its event.
func (t *txn) postMessage(m newMessage) (protocol.Message, error) {
	seq, err := store.NextRoomSeq(t.ctx, t.tx, m.Room)
	if err != nil {
		return protocol.Message{}, err
	}
	if m.Kind == "" {
		m.Kind = protocol.MessageText
	}
	msg := protocol.Message{
		ID: domain.NewID(), OrgID: t.h.Org().ID, RoomID: m.Room, ThreadID: m.Thread, Seq: seq, Author: m.Author,
		Body: m.Body, Kind: m.Kind, Mentions: nonNilMentions(m.Mentions), ProjectIDs: nonNil(m.ProjectIDs), Refs: nonNilRefs(m.Refs),
		ReplyToID: m.ReplyTo, RunID: m.RunID, JobID: m.JobID, ClientKey: m.ClientKey, Revision: 1, CreatedAt: t.h.now(),
		Reactions: []protocol.ReactionSummary{},
	}
	if err := store.InsertMessage(t.ctx, t.tx, msg); err != nil {
		return protocol.Message{}, err
	}
	if err := t.emit(ev{Type: "message.created", Actor: m.Author, Room: m.Room, Thread: m.Thread, Job: m.JobID, Run: m.RunID,
		Cause: m.Cause, Root: m.Root, Payload: msg}); err != nil {
		return protocol.Message{}, err
	}
	if m.Thread != "" {
		if root, err := store.GetMessage(t.ctx, t.tx, m.Thread); err == nil {
			if err := t.emit(ev{Type: "message.updated", Room: m.Room, Payload: root}); err != nil {
				return protocol.Message{}, err
			}
		}
	}
	return msg, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nonNilRefs(r []protocol.Ref) []protocol.Ref {
	if r == nil {
		return []protocol.Ref{}
	}
	return r
}

func nonNilMentions(m []protocol.Mention) []protocol.Mention {
	if m == nil {
		return []protocol.Mention{}
	}
	return m
}

// ---- common lookups ----

func (h *Hub) engineerName(ctx context.Context, q store.Q, id string) string {
	e, err := store.GetEngineer(ctx, q, id)
	if err != nil {
		return "An engineer"
	}
	return e.Name
}

func (h *Hub) nodeName(ctx context.Context, q store.Q, id string) string {
	if id == "" {
		return ""
	}
	n, err := store.GetNode(ctx, q, id)
	if err != nil {
		return "a machine"
	}
	return n.Name
}

// ownerID returns the organisation owner's user ID.
func (h *Hub) ownerID(ctx context.Context, q store.Q) (string, error) {
	users, err := store.ListUsers(ctx, q)
	if err != nil {
		return "", err
	}
	if len(users) == 0 {
		return "", domain.NotFound("No owner has been created yet.")
	}
	return users[0].ID, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
