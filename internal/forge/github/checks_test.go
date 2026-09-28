package github

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestChecksRetainObservedFailuresWhenLaterPagesLoseAccess(t *testing.T) {
	for _, source := range []string{"check-runs", "status"} {
		for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
			for _, outcome := range []string{"failure", "success"} {
				t.Run(fmt.Sprintf("%s/%d/%s", source, status, outcome), func(t *testing.T) {
					f := newFake(t)
					path := "/repos/acme/widgets/commits/" + shaA + "/" + source
					f.hooks["GET "+path] = func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Query().Get("page") == "2" {
							writeJSON(w, status, map[string]string{"message": "Access revoked"})
							return
						}
						w.Header().Set("Link", fmt.Sprintf(`<%s%s?page=2>; rel="next"`, f.srv.URL, path))
						if source == "check-runs" {
							writeJSON(w, 200, map[string]any{"total_count": 2, "check_runs": []any{checkRun("ci/build", "completed", outcome)}})
						} else {
							writeJSON(w, 200, map[string]any{"total_count": 2, "statuses": []any{commitStatus("ci/build", outcome)}})
						}
					}
					other := "check-runs"
					if source == other {
						other = "status"
					}
					f.hooks["GET /repos/acme/widgets/commits/"+shaA+"/"+other] = func(w http.ResponseWriter, r *http.Request) {
						writeJSON(w, status, map[string]string{"message": "Access revoked"})
					}
					got, err := f.connector().Checks(context.Background(), testRepoRef, shaA)
					want, passed, failed := ChecksFailure, 0, 1
					if outcome == "success" {
						want, passed, failed = ChecksUnknown, 1, 0
					}
					if err != nil || got.State != want || got.Total != 1 || got.Failed != failed || got.Passed != passed {
						t.Fatalf("observed %s before access loss: checks=%+v err=%v, want %s with observed item retained", outcome, got, err, want)
					}
				})
			}
		}
	}
}
