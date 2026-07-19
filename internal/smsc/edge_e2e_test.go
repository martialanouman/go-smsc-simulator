package smsc_test

import (
	"encoding/binary"
	"testing"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
	"github.com/martialanouman/go-smsc-simulator/internal/smpptest"
)

// edgeConfig is a healthy virtual SMSC (always success, zero latency) with protocol
// edge-case injection enabled for a single kind on every tick, so a submit_sm_resp is
// deterministically malformed in exactly one way.
func edgeConfig(name string, kind config.EdgeCaseKind) config.VirtualSMSCConfig {
	cfg := healthyConfig(name)
	cfg.Scenario.ProtocolEdgeCasesEnabled = true
	cfg.Scenario.ProtocolEdgeCases = &config.ProtocolEdgeCasesConfig{
		Kinds: []config.EdgeCaseKind{kind},
	}
	return cfg
}

// TestE2E_EdgeCase_Injected drives a real bind+submit against an injecting virtual SMSC
// and asserts the response frame on the wire is malformed exactly as the kind dictates —
// the opt-in path end-to-end (plan §11 / T1).
func TestE2E_EdgeCase_Injected(t *testing.T) {
	t.Parallel()

	t.Run("bad_length", func(t *testing.T) {
		t.Parallel()
		h := startWith(t, edgeConfig("edge-len", config.EdgeCaseBadLength))
		c := smpptest.Dial(t, h.smppAddr)
		c.BindTransceiver(testSystemID, testPassword)

		_, raw := c.SubmitRaw("111", "222", "hi")
		claimed := binary.BigEndian.Uint32(raw[0:4])
		// The header overstates command_length: it claims more than the bytes actually sent.
		if int(claimed) <= len(raw) {
			t.Fatalf("command_length = %d, want > %d bytes actually sent", claimed, len(raw))
		}
	})

	t.Run("unknown_command_id", func(t *testing.T) {
		t.Parallel()
		h := startWith(t, edgeConfig("edge-cmd", config.EdgeCaseUnknownCmdID))
		c := smpptest.Dial(t, h.smppAddr)
		c.BindTransceiver(testSystemID, testPassword)

		_, raw := c.SubmitRaw("111", "222", "hi")
		if cmdID := binary.BigEndian.Uint32(raw[4:8]); cmdID == uint32(smpp.SubmitSMResp) {
			t.Fatalf("command_id = %#x, want a corrupted (non submit_sm_resp) id", cmdID)
		}
	})

	t.Run("bad_sequence", func(t *testing.T) {
		t.Parallel()
		h := startWith(t, edgeConfig("edge-seq", config.EdgeCaseBadSequence))
		c := smpptest.Dial(t, h.smppAddr)
		c.BindTransceiver(testSystemID, testPassword)

		seq, raw := c.SubmitRaw("111", "222", "hi")
		// A well-framed frame whose sequence_number no longer matches the request it answers.
		if got := binary.BigEndian.Uint32(raw[12:16]); got == seq {
			t.Fatalf("sequence_number = %d, want it shifted off the request seq %d", got, seq)
		}
	})
}

// TestE2E_EdgeCase_DisabledIsStrict is the control: with injection off, the same submit
// yields a well-formed ESME_ROK submit_sm_resp the client can frame and decode normally.
func TestE2E_EdgeCase_DisabledIsStrict(t *testing.T) {
	t.Parallel()

	h := startWith(t, healthyConfig("edge-off"))
	c := smpptest.Dial(t, h.smppAddr)
	c.BindTransceiver(testSystemID, testPassword)

	resp := c.Submit("111", "222", "hi")
	if resp.CommandID != smpp.SubmitSMResp || resp.CommandStatus != smpp.StatusROK {
		t.Fatalf("resp = %s/%d, want submit_sm_resp/ESME_ROK", resp.CommandID, resp.CommandStatus)
	}
}
