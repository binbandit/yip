package httpapi

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

func (s *Server) routes() {
	m := s.mux
	a := func(p string, h http.HandlerFunc) { m.HandleFunc(p, withTimeout(s.authed(h))) }

	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	m.HandleFunc("GET /v1/setup", withTimeout(s.getSetup))
	m.HandleFunc("POST /v1/setup", withTimeout(s.postSetup))
	m.HandleFunc("POST /v1/session", withTimeout(s.postSession))
	a("DELETE /v1/session", s.deleteSession)
	a("GET /v1/bootstrap", s.getBootstrap)
	a("PUT /v1/preferences", s.putPreferences)

	a("GET /v1/rooms", s.listRooms)
	a("POST /v1/rooms", s.createRoom)
	a("GET /v1/rooms/{id}", s.getRoom)
	a("PATCH /v1/rooms/{id}", s.patchRoom)
	a("GET /v1/rooms/{id}/members/{engineerId}/preview", s.previewMember)
	a("PUT /v1/rooms/{id}/members/{engineerId}", s.putMember)
	a("DELETE /v1/rooms/{id}/members/{engineerId}", s.deleteMember)
	a("GET /v1/rooms/{id}/messages", s.listMessages)
	a("POST /v1/rooms/{id}/messages", s.postMessage)
	a("POST /v1/rooms/{id}/read", s.markRead)
	a("GET /v1/rooms/{id}/work", s.roomWork)
	a("GET /v1/threads/{id}", s.getThread)
	a("POST /v1/messages/{id}/reactions", s.react)
	a("PATCH /v1/messages/{id}", s.editMessage)
	a("DELETE /v1/messages/{id}", s.deleteMessage)

	a("GET /v1/engineers", s.listEngineers)
	a("POST /v1/engineers", s.createEngineer)
	a("GET /v1/engineers/{id}", s.getEngineer)
	a("PATCH /v1/engineers/{id}", s.patchEngineer)

	a("GET /v1/projects", s.listProjects)
	a("POST /v1/projects", s.createProject)
	a("GET /v1/projects/{id}", s.getProject)
	a("PATCH /v1/projects/{id}", s.patchProject)
	a("PUT /v1/projects/{id}/repos/{repoId}", s.putRepo)
	a("POST /v1/projects/{id}/repos/import", s.importRepo)
	a("PUT /v1/projects/{id}/grants/{engineerId}", s.putGrant)

	a("GET /v1/jobs", s.listJobs)
	a("GET /v1/jobs/{id}", s.getJob)
	a("GET /v1/jobs/{id}/runs/{runId}/activity", s.runActivity)
	a("POST /v1/jobs/{id}/input", s.jobInput)
	a("POST /v1/jobs/{id}/restart", s.restartJob)
	a("POST /v1/jobs/{id}/cancel", s.cancelJob)
	a("POST /v1/jobs/{id}/retry", s.retryJob)
	a("POST /v1/jobs/{id}/accept", s.acceptJob)
	a("GET /v1/reviews/{id}", s.getReview)
	a("POST /v1/pull-requests/link", s.linkPR)
	a("GET /v1/pull-requests/{id}", s.getPR)
	a("GET /v1/runs", s.activeRuns)
	a("GET /v1/questions/{id}", s.getQuestion)
	a("GET /v1/decisions/{id}", s.getDecision)
	a("POST /v1/overview/seen", s.overviewSeen)
	a("GET /v1/approvals/{id}", s.getApproval)
	a("POST /v1/approvals/{id}/decision", s.decideApproval)
	a("POST /v1/questions/{id}/answer", s.answerQuestion)

	a("GET /v1/nodes", s.listNodes)
	a("POST /v1/nodes/enrollments", s.createEnrollment)
	a("POST /v1/nodes/{id}/drain", s.drainNode)
	a("POST /v1/nodes/{id}/stop", s.stopNode)
	a("POST /v1/nodes/{id}/probe", s.probeNode)
	a("POST /v1/nodes/{id}/workspaces/{name}/remove", s.removeWorkspace)
	a("GET /v1/provider-profiles", s.providerProfiles)
	a("PUT /v1/provider-profiles/{id}", s.putProviderProfile)
	a("DELETE /v1/nodes/{id}/credential", s.revokeNode)

	a("GET /v1/decisions", s.listDecisions)
	a("POST /v1/decisions", s.createDecision)
	a("POST /v1/decisions/{id}", s.decideDecision)

	a("GET /v1/overview", s.getOverview)
	a("GET /v1/search", s.search)
	a("GET /v1/artifacts/{id}", s.getArtifact)
	a("GET /v1/diagnostics", s.diagnostics)
	a("GET /v1/diagnostics/bundle", s.diagnosticBundle)
	a("GET /v1/export", s.export)
	m.HandleFunc("GET /v1/events", s.authed(s.events))
	// Forge webhooks authenticate by HMAC signature, not by session.
	m.HandleFunc("POST /v1/forge/github/webhook", withTimeout(s.githubWebhook))

	m.Handle("/", s.static())
}

// ---- setup & session ----

func (s *Server) getSetup(w http.ResponseWriter, r *http.Request) {
	need, err := s.hub.NeedsSetup(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, protocol.SetupStatus{NeedsSetup: need, OrgName: s.hub.Org().Name, Version: s.hub.Config().Version})
}

func (s *Server) postSetup(w http.ResponseWriter, r *http.Request) {
	if err := s.checkOrigin(r); err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.hub.AllowSetupAttempt(clientKey(r)) {
		s.fail(w, r, domain.Limit("Too many setup attempts. Wait a few minutes."))
		return
	}
	req, err := decodeJSON[protocol.SetupRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	u, err := s.hub.Setup(r.Context(), req)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	token, sess, err := s.hub.SignIn(r.Context(), u.Handle, req.Password, clientKey(r), r.UserAgent())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.setCookie(w, token, sess.ExpiresAt)
	writeJSON(w, 201, map[string]any{"user": u})
}

func (s *Server) postSession(w http.ResponseWriter, r *http.Request) {
	if err := s.checkOrigin(r); err != nil {
		s.fail(w, r, err)
		return
	}
	req, err := decodeJSON[protocol.SignInRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	token, sess, err := s.hub.SignIn(r.Context(), req.Handle, req.Password, clientKey(r), r.UserAgent())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.setCookie(w, token, sess.ExpiresAt)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	_ = s.hub.SignOut(r.Context(), sessionFrom(r).ID)
	s.clearCookie(w)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) getBootstrap(w http.ResponseWriter, r *http.Request) {
	b, err := s.hub.Bootstrap(r.Context(), userFrom(r), sessionFrom(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, b)
}

func (s *Server) putPreferences(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.PreferencesRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	u := userFrom(r)
	req.Preferences.LastSeenAt = u.Preferences.LastSeenAt
	if err := s.hub.SetPreferences(r.Context(), u.ID, req.Preferences); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, req.Preferences)
}

// ---- rooms & messages ----

func (s *Server) listRooms(w http.ResponseWriter, r *http.Request) {
	rooms, err := s.hub.ListRooms(r.Context(), userFrom(r).ID)
	respond(s, w, r, rooms, err)
}

func (s *Server) createRoom(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.CreateRoomRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	room, err := s.hub.CreateRoom(r.Context(), userFrom(r).ID, req)
	respondStatus(s, w, r, 201, room, err)
}

func (s *Server) getRoom(w http.ResponseWriter, r *http.Request) {
	room, err := s.hub.GetRoom(r.Context(), userFrom(r).ID, r.PathValue("id"))
	respond(s, w, r, room, err)
}

func (s *Server) patchRoom(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.UpdateRoomRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	room, err := s.hub.UpdateRoom(r.Context(), userFrom(r).ID, r.PathValue("id"), req)
	respond(s, w, r, room, err)
}

func (s *Server) previewMember(w http.ResponseWriter, r *http.Request) {
	p, err := s.hub.PreviewMembership(r.Context(), userFrom(r).ID, r.PathValue("id"), r.PathValue("engineerId"))
	respond(s, w, r, p, err)
}

func (s *Server) putMember(w http.ResponseWriter, r *http.Request) {
	room, err := s.hub.SetMembership(r.Context(), userFrom(r).ID, r.PathValue("id"), r.PathValue("engineerId"), true)
	respond(s, w, r, room, err)
}

func (s *Server) deleteMember(w http.ResponseWriter, r *http.Request) {
	room, err := s.hub.SetMembership(r.Context(), userFrom(r).ID, r.PathValue("id"), r.PathValue("engineerId"), false)
	respond(s, w, r, room, err)
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	page, err := s.hub.ListMessages(r.Context(), userFrom(r).ID, r.PathValue("id"), queryInt(r, "before", 0), int(queryInt(r, "limit", 60)))
	respond(s, w, r, page, err)
}

func (s *Server) postMessage(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.PostMessageRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if req.ClientKey == "" {
		req.ClientKey = r.Header.Get("Idempotency-Key")
	}
	resp, err := s.hub.PostMessage(r.Context(), userFrom(r).ID, r.PathValue("id"), req)
	status := 201
	if resp.Duplicate {
		status = 200
	}
	respondStatus(s, w, r, status, resp, err)
}

func (s *Server) markRead(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.MarkReadRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	err = s.hub.MarkRead(r.Context(), userFrom(r).ID, r.PathValue("id"), req.Seq)
	respond(s, w, r, map[string]bool{"ok": true}, err)
}

func (s *Server) roomWork(w http.ResponseWriter, r *http.Request) {
	rows, err := s.hub.RoomWork(r.Context(), userFrom(r).ID, r.PathValue("id"), r.URL.Query().Get("include") == "replies")
	respond(s, w, r, rows, err)
}

func (s *Server) getThread(w http.ResponseWriter, r *http.Request) {
	msgs, err := s.hub.ListThread(r.Context(), userFrom(r).ID, r.PathValue("id"))
	respond(s, w, r, protocol.MessagePage{Messages: msgs}, err)
}

func (s *Server) react(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.ReactRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	msg, err := s.hub.React(r.Context(), userFrom(r).ID, r.PathValue("id"), req)
	respond(s, w, r, msg, err)
}

func (s *Server) editMessage(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[struct {
		Body string `json:"body"`
	}](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	msg, err := s.hub.EditMessage(r.Context(), userFrom(r).ID, r.PathValue("id"), req.Body)
	respond(s, w, r, msg, err)
}

func (s *Server) deleteMessage(w http.ResponseWriter, r *http.Request) {
	err := s.hub.DeleteMessage(r.Context(), userFrom(r).ID, r.PathValue("id"))
	respond(s, w, r, map[string]bool{"ok": true}, err)
}

// ---- engineers & projects ----

func (s *Server) listEngineers(w http.ResponseWriter, r *http.Request) {
	es, err := s.hub.ListEngineers(r.Context())
	respond(s, w, r, es, err)
}

func (s *Server) createEngineer(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.CreateEngineerRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	e, err := s.hub.CreateEngineer(r.Context(), userFrom(r).ID, req)
	respondStatus(s, w, r, 201, e, err)
}

func (s *Server) getEngineer(w http.ResponseWriter, r *http.Request) {
	e, versions, err := s.hub.GetEngineer(r.Context(), r.PathValue("id"))
	respond(s, w, r, map[string]any{"engineer": e, "versions": versions}, err)
}

func (s *Server) patchEngineer(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.UpdateEngineerRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	e, err := s.hub.UpdateEngineer(r.Context(), userFrom(r).ID, r.PathValue("id"), req)
	respond(s, w, r, e, err)
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	ps, err := s.hub.ListProjects(r.Context())
	respond(s, w, r, ps, err)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.CreateProjectRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.hub.CreateProject(r.Context(), userFrom(r).ID, req)
	respondStatus(s, w, r, 201, p, err)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.hub.GetProject(r.Context(), r.PathValue("id"))
	respond(s, w, r, p, err)
}

func (s *Server) patchProject(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.UpdateProjectRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.hub.UpdateProject(r.Context(), userFrom(r).ID, r.PathValue("id"), req)
	respond(s, w, r, p, err)
}

func (s *Server) putRepo(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.PutRepoRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	repoID := r.PathValue("repoId")
	if repoID == "new" {
		repoID = ""
	}
	p, err := s.hub.PutRepo(r.Context(), userFrom(r).ID, r.PathValue("id"), repoID, req)
	respond(s, w, r, p, err)
}

func (s *Server) putGrant(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.PutGrantRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.hub.PutGrant(r.Context(), userFrom(r).ID, r.PathValue("id"), r.PathValue("engineerId"), req)
	respond(s, w, r, p, err)
}

// ---- work ----

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	f := store.JobFilter{OwnerID: r.URL.Query().Get("owner"), ProjectID: r.URL.Query().Get("project"), Limit: int(queryInt(r, "limit", 200))}
	for _, st := range strings.Split(r.URL.Query().Get("state"), ",") {
		if st != "" {
			f.States = append(f.States, protocol.JobState(st))
		}
	}
	jobs, err := s.hub.ListJobs(r.Context(), userFrom(r).ID, f)
	respond(s, w, r, jobs, err)
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	d, err := s.hub.JobDetail(r.Context(), userFrom(r).ID, r.PathValue("id"))
	respond(s, w, r, d, err)
}

func (s *Server) runActivity(w http.ResponseWriter, r *http.Request) {
	acts, err := s.hub.RunActivity(r.Context(), userFrom(r).ID, r.PathValue("id"), r.PathValue("runId"))
	respond(s, w, r, acts, err)
}

func (s *Server) removeWorkspace(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.CleanupWorkspaceRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	n, err := s.hub.RemoveWorkspace(r.Context(), userFrom(r).ID, r.PathValue("id"), r.PathValue("name"), req)
	respond(s, w, r, n, err)
}

// importRepo takes a raw git bundle body (?name=&branch=&repo= to refresh).
func (s *Server) importRepo(w http.ResponseWriter, r *http.Request) {
	body := http.MaxBytesReader(w, r.Body, hub.MaxArtifactBytes+1)
	q := r.URL.Query()
	p, err := s.hub.ImportRepo(r.Context(), userFrom(r).ID, r.PathValue("id"), q.Get("repo"), q.Get("name"), q.Get("branch"), body)
	respond(s, w, r, p, err)
}

func (s *Server) restartJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.hub.RestartWithInput(r.Context(), userFrom(r).ID, r.PathValue("id"))
	respond(s, w, r, j, err)
}

func (s *Server) jobInput(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.JobInputRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	resp, err := s.hub.AddJobInput(r.Context(), userFrom(r).ID, r.PathValue("id"), req)
	respond(s, w, r, resp, err)
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.CancelJobRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	j, err := s.hub.CancelJob(r.Context(), userFrom(r).ID, r.PathValue("id"), req)
	respond(s, w, r, j, err)
}

func (s *Server) retryJob(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.RetryJobRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	j, err := s.hub.RetryJob(r.Context(), userFrom(r).ID, r.PathValue("id"), req)
	respond(s, w, r, j, err)
}

func (s *Server) acceptJob(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.AcceptJobRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	j, err := s.hub.AcceptJob(r.Context(), userFrom(r).ID, r.PathValue("id"), req)
	respond(s, w, r, j, err)
}

func (s *Server) getReview(w http.ResponseWriter, r *http.Request) {
	rv, err := s.hub.GetReview(r.Context(), userFrom(r).ID, r.PathValue("id"))
	respond(s, w, r, rv, err)
}

func (s *Server) linkPR(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.LinkPullRequestRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	pr, err := s.hub.LinkPullRequest(r.Context(), userFrom(r).ID, req)
	respond(s, w, r, pr, err)
}

func (s *Server) getPR(w http.ResponseWriter, r *http.Request) {
	pr, err := s.hub.GetPullRequest(r.Context(), userFrom(r).ID, r.PathValue("id"), r.URL.Query().Get("refresh") == "1")
	respond(s, w, r, pr, err)
}

func (s *Server) getApproval(w http.ResponseWriter, r *http.Request) {
	a, err := s.hub.GetApproval(r.Context(), userFrom(r).ID, r.PathValue("id"))
	respond(s, w, r, a, err)
}

func (s *Server) decideApproval(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.ApprovalDecisionRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	a, err := s.hub.DecideApproval(r.Context(), userFrom(r).ID, r.PathValue("id"), req)
	respond(s, w, r, a, err)
}

func (s *Server) answerQuestion(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.AnswerQuestionRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	resp, err := s.hub.AnswerQuestion(r.Context(), userFrom(r).ID, r.PathValue("id"), req)
	respond(s, w, r, resp, err)
}

// ---- machines ----

func (s *Server) listNodes(w http.ResponseWriter, r *http.Request) {
	ns, err := s.hub.ListNodes(r.Context())
	respond(s, w, r, ns, err)
}

func (s *Server) createEnrollment(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.CreateEnrollmentRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	en, err := s.hub.CreateEnrollment(r.Context(), userFrom(r).ID, req)
	respondStatus(s, w, r, 201, en, err)
}

func (s *Server) drainNode(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.NodeActionRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	n, err := s.hub.SetDraining(r.Context(), userFrom(r).ID, r.PathValue("id"), req.Drain)
	respond(s, w, r, n, err)
}

func (s *Server) providerProfiles(w http.ResponseWriter, r *http.Request) {
	out, err := s.hub.ProviderProfiles(r.Context())
	respond(s, w, r, out, err)
}

func (s *Server) putProviderProfile(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.ProviderProfileRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.hub.SetProviderConcurrency(r.Context(), userFrom(r).ID, r.PathValue("id"), req.MaxConcurrency)
	respond(s, w, r, out, err)
}

func (s *Server) probeNode(w http.ResponseWriter, r *http.Request) {
	err := s.hub.ProbeNode(r.Context(), userFrom(r).ID, r.PathValue("id"))
	respond(s, w, r, map[string]bool{"ok": true}, err)
}

func (s *Server) stopNode(w http.ResponseWriter, r *http.Request) {
	err := s.hub.StopNodeWork(r.Context(), userFrom(r).ID, r.PathValue("id"))
	respond(s, w, r, map[string]bool{"ok": true}, err)
}

func (s *Server) revokeNode(w http.ResponseWriter, r *http.Request) {
	err := s.hub.RevokeNode(r.Context(), userFrom(r).ID, r.PathValue("id"))
	respond(s, w, r, map[string]bool{"ok": true}, err)
}

// ---- knowledge, overview, search ----

func (s *Server) listDecisions(w http.ResponseWriter, r *http.Request) {
	ds, err := s.hub.ListDecisions(r.Context(), userFrom(r).ID, r.URL.Query().Get("status"))
	respond(s, w, r, ds, err)
}

func (s *Server) createDecision(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.DecisionRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d, err := s.hub.CreateDecision(r.Context(), userFrom(r).ID, req)
	respondStatus(s, w, r, 201, d, err)
}

func (s *Server) decideDecision(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[protocol.DecisionActionRequest](w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d, err := s.hub.DecideDecision(r.Context(), userFrom(r).ID, r.PathValue("id"), req)
	respond(s, w, r, d, err)
}

func (s *Server) getOverview(w http.ResponseWriter, r *http.Request) {
	ov, err := s.hub.Overview(r.Context(), userFrom(r).ID, r.URL.Query().Get("seen") == "1")
	respond(s, w, r, ov, err)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	res, err := s.hub.Search(r.Context(), userFrom(r).ID, r.URL.Query().Get("q"), r.URL.Query().Get("room"), r.URL.Query().Get("project"))
	respond(s, w, r, res, err)
}

func (s *Server) getArtifact(w http.ResponseWriter, r *http.Request) {
	a, f, err := s.hub.OpenArtifact(r.Context(), userFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer f.Close()
	ct := a.ContentType
	inline := strings.HasPrefix(ct, "text/") || ct == "application/json"
	if !inline || r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", safeName(a.Name)))
	}
	if inline {
		ct = "text/plain; charset=utf-8" // never render uploaded HTML
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Artifact-Sha256", a.Hash)
	http.ServeContent(w, r, "", a.CreatedAt, f)
}

func safeName(n string) string {
	n = strings.Map(func(r rune) rune {
		if r < 32 || r == '"' || r == '\\' || r == '/' {
			return '_'
		}
		return r
	}, n)
	if n == "" {
		return "artifact"
	}
	return n
}

func (s *Server) diagnosticBundle(w http.ResponseWriter, r *http.Request) {
	b, err := s.hub.DiagnosticBundle(r.Context())
	respond(s, w, r, b, err)
}

func (s *Server) diagnostics(w http.ResponseWriter, r *http.Request) {
	d, err := s.hub.Diagnostics(r.Context())
	respond(s, w, r, d, err)
}

// export writes a zip of messages, jobs, decisions, and the artifact
// manifest in documented JSON formats. No hosted service is involved.
func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"yip-export-%s.zip\"", time.Now().UTC().Format("20060102-1504")))
	zw := zip.NewWriter(w)
	defer zw.Close()
	if err := s.hub.Export(r.Context(), userFrom(r).ID, func(name string, v any) error {
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}, func(name string, rd io.Reader) error {
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, rd)
		return err
	}); err != nil {
		s.log.Error("export failed", "err", err)
	}
}

func respond(s *Server, w http.ResponseWriter, r *http.Request, v any, err error) {
	respondStatus(s, w, r, 200, v, err)
}

func respondStatus(s *Server, w http.ResponseWriter, r *http.Request, status int, v any, err error) {
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, status, v)
}

var _ = hub.ProviderLabel

func (s *Server) githubWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 5<<20))
	if err != nil {
		s.fail(w, r, domain.Invalid("Webhook body too large."))
		return
	}
	headers := map[string]string{}
	for _, k := range []string{"X-Hub-Signature-256", "X-GitHub-Delivery", "X-GitHub-Event", "Content-Type"} {
		headers[k] = r.Header.Get(k)
	}
	if err := s.hub.HandleForgeWebhook(r.Context(), "github", headers, body); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) activeRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.hub.ActiveRuns(r.Context(), userFrom(r).ID)
	respond(s, w, r, runs, err)
}

func (s *Server) getQuestion(w http.ResponseWriter, r *http.Request) {
	q, err := s.hub.GetQuestion(r.Context(), userFrom(r).ID, r.PathValue("id"))
	respond(s, w, r, q, err)
}

func (s *Server) getDecision(w http.ResponseWriter, r *http.Request) {
	d, err := s.hub.GetDecision(r.Context(), userFrom(r).ID, r.PathValue("id"))
	respond(s, w, r, d, err)
}

func (s *Server) overviewSeen(w http.ResponseWriter, r *http.Request) {
	err := s.hub.MarkOverviewSeen(r.Context(), userFrom(r).ID)
	respond(s, w, r, map[string]bool{"ok": true}, err)
}
