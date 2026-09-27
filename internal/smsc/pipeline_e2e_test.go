package smsc_test

import (
	"strings"
	"testing"
	"time"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
	"github.com/martialanouman/go-smsc-simulator/internal/smpptest"
)

// A client window of N submits under served latency L completes in ~L, not N*L: the
// latency holds back each response, never the read loop. An enquire_link sent meanwhile is
// answered at once, and each DLR (delay 1 tick) still trails its own submit_sm_resp.
func TestPipeline_WindowUnderLatency(t *testing.T) {
	t.Parallel()
	const latency, window = 300 * time.Millisecond, 5

	cfg := dlrConfig("carrier-pipeline", pu64(7), 1, config.DLROutcomeWeights{Delivered: 1}, 10_000)
	cfg.Scenario.Latency.Params.MS = pu64(uint64(latency / time.Millisecond))
	h := startWith(t, cfg)
	c := smpptest.Dial(t, h.smppAddr)
	c.BindTransceiver(testSystemID, testPassword)

	start := time.Now()
	for range window {
		c.SubmitAsync("33600000000", "33611111111", "hi")
	}
	if r := c.EnquireLink(); r.CommandID != smpp.EnquireLinkResp {
		t.Fatalf("got %v while latency pending, want enquire_link_resp first", r.CommandID)
	}
	if d := time.Since(start); d >= latency {
		t.Fatalf("enquire_link answered after %v, blocked behind served latency", d)
	}

	answered := map[string]bool{}
	for len(answered) < window {
		p := c.ReadWithin(2 * time.Second)
		switch b := p.Body.(type) {
		case *smpp.SubmitResp:
			answered[b.MessageID] = true
		case *smpp.Message: // DLR for an earlier submit; its resp must already be out
			id, _, _ := strings.Cut(strings.TrimPrefix(string(b.ShortMessage), "id:"), " ")
			if !answered[id] {
				t.Fatalf("DLR for %s arrived before its submit_sm_resp", id)
			}
		}
	}
	if d := time.Since(start); d > 2*latency {
		t.Fatalf("window of %d took %v, want < %v (responses serialised)", window, d, 2*latency)
	}
}
