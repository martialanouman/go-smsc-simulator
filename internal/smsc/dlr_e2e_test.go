package smsc_test

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
	"github.com/martialanouman/go-smsc-simulator/internal/smpptest"
)

// dlrConfig builds a healthy virtual SMSC (every submit succeeds, so every submit yields
// a DLR) with DLR generation on: a fixed tick delay, an outcome mix, and a short
// quiescence window so the flush fires quickly in tests.
func dlrConfig(name string, seed *uint64, delayTicks uint64, w config.DLROutcomeWeights, quiescenceMs uint64) config.VirtualSMSCConfig {
	cfg := baseConfig(name, seed, config.ScenarioConfig{
		Profile: config.ProfileHealthy,
		Latency: config.LatencyConfig{Distribution: config.LatencyFixed, Params: config.LatencyParams{MS: pu64(0)}},
		DLR: &config.DLRConfig{
			Delay:          config.DLRDelay{Distribution: config.LatencyFixed, Ticks: pu64(delayTicks)},
			OutcomeWeights: w,
			Clock:          config.ClockLogical,
		},
	})
	cfg.QuiescenceFlushMs = pu64(quiescenceMs)
	return cfg
}

// statOf extracts the "stat:" keyword from a delivery-receipt deliver_sm.
func statOf(t *testing.T, pdu *smpp.PDU) string {
	t.Helper()
	m, ok := pdu.Body.(*smpp.Message)
	if !ok {
		t.Fatalf("deliver_sm body = %T, want *smpp.Message", pdu.Body)
	}
	txt := string(m.ShortMessage)
	i := strings.Index(txt, "stat:")
	if i < 0 {
		t.Fatalf("no stat: field in receipt %q", txt)
	}
	rest := txt[i+len("stat:"):]
	if j := strings.IndexByte(rest, ' '); j >= 0 {
		return rest[:j]
	}
	return rest
}

// TestE2E_DLR_QuiescenceFlush is invariant (d): a batch submitted then left in silence
// has its pending DLRs flushed — in tick (submit) order — once the traffic stops. The
// delay is larger than the batch so nothing drains during it; every DLR waits for the
// flush.
func TestE2E_DLR_QuiescenceFlush(t *testing.T) {
	t.Parallel()

	h := startWith(t, dlrConfig("carrier-dlr-flush", pu64(1), 100, config.DLROutcomeWeights{Delivered: 1}, 60))
	client := smpptest.Dial(t, h.smppAddr)
	client.BindTransceiver(testSystemID, testPassword)

	const n = 5
	ids := make([]string, n)
	for i := range ids {
		resp := client.Submit("33600000000", "33611111111", "m")
		body, ok := resp.Body.(*smpp.SubmitResp)
		if !ok || resp.CommandStatus != smpp.StatusROK {
			t.Fatalf("submit %d = %s/%d, want submit_sm_resp/ROK", i, resp.CommandID, resp.CommandStatus)
		}
		ids[i] = body.MessageID
	}

	dlrs := client.DrainDeliverSMs(300 * time.Millisecond)
	if len(dlrs) != n {
		t.Fatalf("got %d DLRs after flush, want %d", len(dlrs), n)
	}
	for i, pdu := range dlrs {
		m := pdu.Body.(*smpp.Message)
		if m.ESMClass != smpp.ESMClassDeliveryReceipt {
			t.Errorf("DLR %d esm_class = 0x%02x, want 0x04", i, m.ESMClass)
		}
		// Flushed in tick order, which is submit order: DLR i correlates to submit i.
		if got := string(m.ShortMessage); !strings.Contains(got, "id:"+ids[i]) {
			t.Errorf("DLR %d = %q, want id:%s (tick order)", i, got, ids[i])
		}
	}
}

// TestE2E_DLR_NormalDrainOnClockAdvance is the other drain path (voie a): with a small
// delay and continuous traffic, a DLR fires as the clock reaches its due tick — no
// quiescence flush needed. Submitting past the delay releases the earliest DLRs mid-run.
func TestE2E_DLR_NormalDrainOnClockAdvance(t *testing.T) {
	t.Parallel()

	// delay 2 ticks: the DLR for submit k comes due at tick k+2, released when submit k+2
	// advances the clock. A generous quiescence window ensures any receipt seen mid-run
	// came from the clock advancing, not a flush.
	h := startWith(t, dlrConfig("carrier-dlr-drain", pu64(1), 2, config.DLROutcomeWeights{Delivered: 1}, 5000))
	client := smpptest.Dial(t, h.smppAddr)
	client.BindTransceiver(testSystemID, testPassword)

	// Submit asynchronously: with the clock advancing, receipts interleave with the
	// submit responses, which the synchronous Submit could not disentangle.
	const n = 6
	for i := 0; i < n; i++ {
		client.SubmitAsync("33600000000", "33611111111", "m")
	}
	// DLRs for submits 1..n-2 come due at ticks 3..n and are released by the advancing
	// clock — no flush involved (the quiescence window is 5 s). Collect those n-2 receipts
	// well within it, proving the normal drain path.
	dlrs := client.CollectDeliverSMs(n-2, 1*time.Second)
	first := dlrs[0].Body.(*smpp.Message)
	if first.ESMClass != smpp.ESMClassDeliveryReceipt {
		t.Fatalf("esm_class = 0x%02x, want 0x04", first.ESMClass)
	}
	if got := string(first.ShortMessage); !strings.Contains(got, "id:1-0001") {
		t.Errorf("first drained DLR = %q, want the earliest submit id:1-0001", got)
	}
}

// TestE2E_DLR_Correlation checks each receipt references its origin submit's message_id,
// in both the receipt text ("id:") and the receipted_message_id TLV, and that the
// receipt flows back with the addresses swapped.
func TestE2E_DLR_Correlation(t *testing.T) {
	t.Parallel()

	h := startWith(t, dlrConfig("carrier-dlr-corr", pu64(1), 1, config.DLROutcomeWeights{Delivered: 1}, 50))
	client := smpptest.Dial(t, h.smppAddr)
	client.BindTransceiver(testSystemID, testPassword)

	resp := client.Submit("33600000000", "33611111111", "hi")
	msgID := resp.Body.(*smpp.SubmitResp).MessageID

	dlr := client.ReadDeliverSM(300 * time.Millisecond)
	m := dlr.Body.(*smpp.Message)

	if got := string(m.ShortMessage); !strings.Contains(got, "id:"+msgID) {
		t.Errorf("receipt text %q missing id:%s", got, msgID)
	}
	var tlvID string
	for _, tlv := range m.TLVs {
		if tlv.Tag == smpp.TagReceiptedMessageID {
			tlvID = string(bytes.TrimRight(tlv.Value, "\x00"))
		}
	}
	if tlvID != msgID {
		t.Errorf("receipted_message_id TLV = %q, want %q", tlvID, msgID)
	}
	if m.SourceAddr != "33611111111" || m.DestAddr != "33600000000" {
		t.Errorf("DLR addresses src=%q dst=%q, want origin dest/source swapped", m.SourceAddr, m.DestAddr)
	}
}

// TestE2E_DLR_DeterministicReplay is invariant (a) extended to the DLR channel: at a
// fixed seed, two independent runs of the same fixture produce the identical sequence of
// DLR outcomes in the identical tick order.
func TestE2E_DLR_DeterministicReplay(t *testing.T) {
	t.Parallel()

	run := func() []string {
		h := startWith(t, dlrConfig("carrier-dlr-det", pu64(42), 50,
			config.DLROutcomeWeights{Delivered: 5, Failed: 3, Expired: 2}, 60))
		client := smpptest.Dial(t, h.smppAddr)
		client.BindTransceiver(testSystemID, testPassword)

		const n = 40
		for i := 0; i < n; i++ {
			client.Submit("33600000000", "33611111111", "m")
		}
		dlrs := client.DrainDeliverSMs(250 * time.Millisecond)
		if len(dlrs) != n {
			t.Fatalf("got %d DLRs, want %d", len(dlrs), n)
		}
		stats := make([]string, len(dlrs))
		for i, pdu := range dlrs {
			stats[i] = statOf(t, pdu)
		}
		return stats
	}

	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("DLR outcome sequence not reproducible at fixed seed:\n%v\n%v", first, second)
	}
	// Sanity: the weighted mix actually exercised more than one outcome kind.
	kinds := map[string]bool{}
	for _, s := range first {
		kinds[s] = true
	}
	if len(kinds) < 2 {
		t.Fatalf("expected a mix of DLR outcomes, saw only %v", kinds)
	}
}

// TestE2E_DLR_TransmitterOriginDropped is the "unknown/bad mapping" case: a DLR whose
// origin bind is transmitter-only has no return path, so it is counted and logged, never
// emitted silently.
func TestE2E_DLR_TransmitterOriginDropped(t *testing.T) {
	t.Parallel()

	const name = "carrier-dlr-tx"
	h := startWith(t, dlrConfig(name, pu64(1), 1, config.DLROutcomeWeights{Delivered: 1}, 50))
	client := smpptest.Dial(t, h.smppAddr)
	client.BindTransmitter(testSystemID, testPassword) // may submit, may not receive

	resp := client.Submit("33600000000", "33611111111", "hi")
	if resp.CommandStatus != smpp.StatusROK {
		t.Fatalf("submit status = %d, want ROK", resp.CommandStatus)
	}
	// No deliver_sm may arrive on a transmitter bind, even past the quiescence window.
	client.ExpectNoResponse(200 * time.Millisecond)

	dropped, ok := h.engine.DLRsDropped(name)
	if !ok || dropped != 1 {
		t.Fatalf("DLRsDropped(%q) = (%d, %v), want (1, true)", name, dropped, ok)
	}
}
