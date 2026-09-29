package integration

import (
	"testing"

	"github.com/binbandit/yip/protocol"
)

func TestEmptyProviderReportHasReceipt(t *testing.T) {
	e := newEnv(t, envOptions{})
	n := e.fakeNode("empty provider runner")
	n.send(e, protocol.EvCapabilities, "", 0, protocol.RunnerCapabilities{Slots: 1, Providers: []protocol.ProviderInstallation{}})
	var reports int
	err := e.hub.Store().R().QueryRowContext(e.ctx, `SELECT COUNT(*) FROM events
		WHERE type = 'node.updated' AND json_extract(payload, '$.id') = ?
		AND json_extract(payload, '$.capabilitiesReported') = 1
		AND json_array_length(json_extract(payload, '$.providers')) = 0`, n.id).Scan(&reports)
	if err != nil {
		t.Fatal(err)
	}
	if reports != 1 {
		t.Fatalf("empty provider report receipt count = %d, want 1", reports)
	}
}
