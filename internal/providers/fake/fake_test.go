package fake

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/binbandit/yip/internal/providers"
)

// Ending the event stream and receiving owner input can overlap. Delivery
// has its own runner receipt; it must not write to the completed stream.
func TestLateInputDoesNotWriteToClosedStream(t *testing.T) {
	s := &session{ctx: context.Background(), events: make(chan providers.Event), done: make(chan struct{}), vars: map[string]any{}}
	close(s.events)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.SendInput(context.Background(), "Keep the contract")
			if err != nil && !errors.Is(err, providers.ErrUnsupported) {
				t.Errorf("late input: %v", err)
			}
		}()
	}
	close(s.done)
	wg.Wait()
	if _, err := s.SendInput(context.Background(), "Already finished"); !errors.Is(err, providers.ErrUnsupported) {
		t.Fatalf("a finished run must queue input for the next run, got %v", err)
	}
}
