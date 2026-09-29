package integration

import (
	"sync"
	"testing"

	"github.com/binbandit/yip/protocol"
)

func simulationBrowserBurst(n int, request func(int) error) []error {
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = request(i)
		}()
	}
	close(start)
	wg.Wait()
	return errs
}

// These clients share the owner's session, as multiple tabs or devices do.
// This tests concurrent delivery, not multi-human tenancy or RBAC.
func TestSimulationConcurrentDuplicateMessagesSurviveRestart(t *testing.T) {
	e := newEnv(t, envOptions{})
	path := "/v1/rooms/" + e.roomID("Security") + "/messages"
	req := protocol.PostMessageRequest{Body: "@Mira check this once", ClientKey: "concurrent-owner-message",
		Mentions: []protocol.Mention{{Kind: protocol.ActorEngineer, ID: e.engineerID("mira")}}}
	messageID := ""
	for wave := 0; wave < 2; wave++ {
		if wave == 1 {
			e.restartHub()
		}
		responses := make([]protocol.PostMessageResponse, 32)
		errs := simulationBrowserBurst(len(responses), func(i int) error {
			return e.c.do("POST", path, req, &responses[i])
		})
		first, dispatches := 0, 0
		for i, err := range errs {
			if err != nil {
				t.Fatalf("wave %d client %d failed: %v", wave, i, err)
			}
			response := responses[i]
			if messageID == "" {
				messageID = response.Message.ID
			}
			if response.Message.ID != messageID {
				t.Fatalf("concurrent retries created different messages: %s / %s", messageID, response.Message.ID)
			}
			if !response.Duplicate {
				first++
			}
			dispatches += len(response.Dispatched)
		}
		want := 1 - wave
		if first != want || dispatches != want {
			t.Fatalf("wave %d: first responses=%d dispatches=%d; wanted %d each", wave, first, dispatches, want)
		}
		jobs := e.jobsWithReplies()
		if len(jobs) != 1 || jobs[0].Source.MessageID != messageID || len(e.jobDetail(jobs[0].ID).Runs) != 1 {
			t.Fatalf("the burst must persist one assignment and one attempt: %+v", jobs)
		}
		messages := 0
		for _, m := range e.messages("Security") {
			if m.ID == messageID {
				messages++
			}
		}
		if messages != 1 {
			t.Fatalf("canonical message appears %d times", messages)
		}
	}
}
