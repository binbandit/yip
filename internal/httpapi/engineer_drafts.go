package httpapi

import (
	"net/http"

	"github.com/binbandit/yip/protocol"
)

func (s *Server) createEngineerDraft(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeJSON[protocol.CreateEngineerDraftRequest](s, w, r)
	if !ok {
		return
	}
	draft, err := s.hub.CreateEngineerDraft(r.Context(), userFrom(r).ID, req)
	respond(s, w, r, draft, err)
}

func (s *Server) getEngineerDraft(w http.ResponseWriter, r *http.Request) {
	draft, err := s.hub.GetEngineerDraft(r.Context(), userFrom(r).ID, r.PathValue("id"))
	respond(s, w, r, draft, err)
}

func (s *Server) cancelEngineerDraft(w http.ResponseWriter, r *http.Request) {
	draft, err := s.hub.CancelEngineerDraft(r.Context(), userFrom(r).ID, r.PathValue("id"))
	respond(s, w, r, draft, err)
}
