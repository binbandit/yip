package hub

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/binbandit/yip/protocol"
)

func TestConcurrentSchedulerTicks(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h, err := Open(ctx, Config{DataDir: t.TempDir(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	secret, _, err := h.IssueBootstrapSecret(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Setup(ctx, protocol.SetupRequest{BootstrapSecret: secret, OrgName: "Team", Name: "Owner", Handle: "owner", Password: "a-long-test-password"}); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			<-start
			h.Tick(ctx)
		})
	}
	close(start)
	wg.Wait()
}
