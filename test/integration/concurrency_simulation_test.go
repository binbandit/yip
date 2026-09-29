package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	manifest "github.com/binbandit/yip/internal/context"
	"github.com/binbandit/yip/internal/providers/fake"
	"github.com/binbandit/yip/internal/store"
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
	t.Parallel()
	e := newEnv(t, envOptions{noRunner: true})
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

func TestSimulationConcurrentRoomBurstsKeepDestinations(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{noRunner: true, slots: 6, director: func(m *manifest.Manifest) json.RawMessage {
		if m.Job.Kind == "reply" {
			return script(fake.Step{Sleep: "20ms"}, fake.Step{Final: "Receipt: " + m.Request.Body})
		}
		return nil
	}})
	type destination struct{ room, thread, engineer string }
	var destinations []destination
	for i, handle := range []string{"mira", "oren", "pip"} {
		var room protocol.Room
		e.c.must("POST", "/v1/rooms", protocol.CreateRoomRequest{Name: fmt.Sprintf("Simulation team %d", i), Private: i != 2,
			ReplyMode: protocol.ReplyModeQuiet, EngineerIDs: []string{e.engineerID(handle)}}, &room)
		root := e.post(room.Name, "Coordination thread", nil, nil)
		destinations = append(destinations, destination{room.ID, root.Message.ID, e.engineerID(handle)})
	}
	requests := make([]protocol.PostMessageRequest, 24)
	responses := make([]protocol.PostMessageResponse, len(requests))
	for i := range requests {
		d := destinations[i%len(destinations)]
		requests[i] = protocol.PostMessageRequest{Body: fmt.Sprintf("Team %d request %02d", i%len(destinations), i), ClientKey: fmt.Sprintf("team-burst-%02d", i),
			Mentions: []protocol.Mention{{Kind: protocol.ActorEngineer, ID: d.engineer}}}
		if i%2 == 0 {
			requests[i].ThreadID = d.thread
		}
	}
	errs := simulationBrowserBurst(len(requests), func(i int) error {
		return e.c.do("POST", "/v1/rooms/"+destinations[i%len(destinations)].room+"/messages", requests[i], &responses[i])
	})
	byMessage := make(map[string]int, len(responses))
	for i, err := range errs {
		if err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
		if responses[i].Duplicate || len(responses[i].Dispatched) != 1 {
			t.Fatalf("distinct request %d was lost or misrouted: %+v", i, responses[i])
		}
		byMessage[responses[i].Message.ID] = i
	}
	if len(byMessage) != len(requests) {
		t.Fatalf("distinct sends persisted only %d messages", len(byMessage))
	}
	e.startRunner()
	e.waitFor("all three teams' replies", 60*time.Second, func() bool {
		jobs := e.jobsWithReplies()
		if len(jobs) != len(requests) {
			return false
		}
		for _, job := range jobs {
			if job.State != protocol.JobCompleted {
				return false
			}
		}
		return true
	})
	byJob := make(map[string]int, len(requests))
	for _, job := range e.jobsWithReplies() {
		i, ok := byMessage[job.Source.MessageID]
		if !ok {
			t.Fatalf("an assignment has an unknown source: %+v", job.Source)
		}
		byJob[job.ID] = i
		want := destinations[i%len(destinations)]
		if job.Source.RoomID != want.room || job.Source.ThreadID != requests[i].ThreadID || job.OwnerID != want.engineer {
			t.Fatalf("request %d changed destination or engineer: %+v", i, job)
		}
		runs := e.jobDetail(job.ID).Runs
		if len(runs) != 1 || runs[0].Destination != job.Source || runs[0].State != protocol.RunSucceeded {
			t.Fatalf("request %d duplicated or lost its immutable run destination: %+v", i, runs)
		}
	}
	for _, dest := range destinations {
		var page protocol.MessagePage
		e.c.must("GET", "/v1/rooms/"+dest.room+"/messages?limit=200", nil, &page)
		replies := 0
		for _, message := range page.Messages {
			if !strings.HasPrefix(message.Body, "Receipt: ") {
				continue
			}
			i, ok := byMessage[message.ReplyToID]
			if !ok || message.Body != "Receipt: "+requests[i].Body || message.ThreadID != requests[i].ThreadID || message.RoomID != destinations[i%len(destinations)].room {
				t.Fatalf("a concurrent reply landed on the wrong message or thread: %+v", message)
			}
			replies++
		}
		// The room timeline includes top-level messages only; half of each
		// team's replies live in its thread and are verified separately.
		var thread protocol.MessagePage
		e.c.must("GET", "/v1/threads/"+dest.thread, nil, &thread)
		for _, message := range thread.Messages {
			if !strings.HasPrefix(message.Body, "Receipt: ") {
				continue
			}
			i, ok := byJob[message.JobID]
			if !ok || message.Body != "Receipt: "+requests[i].Body || message.ThreadID != dest.thread || message.RoomID != dest.room {
				t.Fatalf("a threaded reply crossed team boundaries: %+v", message)
			}
			replies++
		}
		if replies != 8 {
			t.Fatalf("team %s received %d replies; wanted 8", dest.room, replies)
		}
	}
}

// Two machines sharing one provider account must retain one active slot
// while many browser clients race to cancel and explicitly retry a job.
func TestSimulationConcurrentRetryAndCancelKeepSingleAccountSlot(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		if replyTo(m, "Engineering", "long investigation") {
			return script(toolStep("work_create", map[string]any{"title": "Concurrent recovery investigation", "objective": "Investigate recovery", "kind": "investigation", "project": "Beacon"}, ""))
		}
		return script(fake.Step{Status: "Working until cancelled"}, fake.Step{Fault: "hang"})
	}})
	var profiles []protocol.ProviderProfile
	e.c.must("GET", "/v1/provider-profiles", nil, &profiles)
	for _, profile := range profiles {
		if profile.Provider == "fake" {
			e.c.must("PUT", "/v1/provider-profiles/"+profile.ID, protocol.ProviderProfileRequest{MaxConcurrency: 1}, nil)
		}
	}
	stopSecond := e.startNamedRunner("second-runner", "Second simulation machine", 3)
	t.Cleanup(stopSecond)
	e.post("Engineering", "@Pip start the long investigation", []string{"pip"}, nil)
	job := e.waitJob("Concurrent recovery investigation", protocol.JobRunning)
	e.waitFor("the first attempt to execute", 15*time.Second, func() bool {
		d := e.jobDetail(job.ID)
		return len(d.Runs) == 1 && d.Runs[0].State == protocol.RunRunning
	})
	for _, handle := range []string{"mira", "oren"} {
		e.post("Security", "@"+handle+" queued independent work", []string{handle}, nil)
	}
	stopMonitor := make(chan struct{})
	violations := make(chan error, 1)
	var monitor sync.WaitGroup
	monitor.Add(1)
	go func() {
		defer monitor.Done()
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopMonitor:
				return
			case <-ticker.C:
				runs, err := store.RunsInStates(e.ctx, e.hub.Store().R(), protocol.RunOffered, protocol.RunPreparing, protocol.RunRunning, protocol.RunAwaitingInput, protocol.RunStopping)
				if err == nil && len(runs) <= 1 {
					continue
				}
				if err == nil {
					err = fmt.Errorf("one account acquired %d simultaneous leases across two machines", len(runs))
				}
				select {
				case violations <- err:
				default:
				}
			}
		}
	}()
	defer func() { close(stopMonitor); monitor.Wait() }()
	for i, err := range simulationBrowserBurst(16, func(i int) error {
		return e.c.do("POST", "/v1/jobs/"+job.ID+"/cancel", protocol.CancelJobRequest{Reason: "Concurrent stop"}, nil)
	}) {
		if err != nil {
			t.Fatalf("cancel client %d: %v", i, err)
		}
	}
	e.waitFor("the cancelled attempt to stop", 15*time.Second, func() bool {
		d := e.jobDetail(job.ID)
		return d.Job.State == protocol.JobCancelled && len(d.Runs) == 1 && d.Runs[0].State == protocol.RunCancelled
	})
	succeeded := 0
	for i, err := range simulationBrowserBurst(32, func(i int) error {
		return e.c.do("POST", "/v1/jobs/"+job.ID+"/retry", protocol.RetryJobRequest{Reason: "Retry from another browser tab"}, nil)
	}) {
		if err == nil {
			succeeded++
		} else if !isStatus(err, http.StatusConflict) {
			t.Fatalf("retry client %d: %v", i, err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("concurrent retry created %d accepted attempts; wanted one", succeeded)
	}
	detail := e.jobDetail(job.ID)
	if len(detail.Runs) != 2 {
		t.Fatalf("retry burst persisted %d attempts; wanted cancelled attempt plus one retry", len(detail.Runs))
	}
	for _, queued := range e.jobsWithReplies() {
		if queued.Kind == protocol.JobKindReply && queued.State != protocol.JobCompleted {
			e.c.must("POST", "/v1/jobs/"+queued.ID+"/cancel", protocol.CancelJobRequest{Reason: "Release the queued simulation slot"}, nil)
		}
	}
	e.waitFor("the explicit retry to run after queued work releases its slot", 15*time.Second, func() bool {
		for _, run := range e.jobDetail(job.ID).Runs {
			if run.Attempt == 2 && run.State == protocol.RunRunning {
				return true
			}
		}
		return false
	})
	select {
	case err := <-violations:
		t.Fatal(err)
	default:
	}
	if err := e.c.do("POST", "/v1/jobs/"+job.ID+"/retry", protocol.RetryJobRequest{Reason: "Duplicate while running"}, nil); !isStatus(err, http.StatusConflict) {
		t.Fatalf("a running explicit retry accepted another writer: %v", err)
	}
}
