package smsc_test

import (
	"net"
	"testing"

	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
	"github.com/martialanouman/go-smsc-simulator/internal/smpptest"
)

// A bare TCP connect (k8s tcpSocket probe, wait-for-port) or a rejected bind must not
// consume a bind ordinal: the ordinal seeds the per-bind PRNG and the message_id, so a
// probe-dependent value would break seeded replay (invariant a).
func TestBindOrdinal_IgnoresProbesAndRejectedBinds(t *testing.T) {
	t.Parallel()
	h := start(t, "carrier-ordinal")

	probe, err := net.Dial("tcp", h.smppAddr)
	if err != nil {
		t.Fatal(err)
	}
	_ = probe.Close()

	bad := smpptest.Dial(t, h.smppAddr)
	bad.BindTransceiver(testSystemID, "wrong")
	bad.ExpectClosed()

	c := smpptest.Dial(t, h.smppAddr)
	c.BindTransceiver(testSystemID, testPassword)
	resp := c.Submit("33600000000", "33611111111", "hi")
	if body, ok := resp.Body.(*smpp.SubmitResp); !ok || body.MessageID != "1-0001" {
		t.Fatalf("message_id = %+v, want 1-0001 (probe/rejected bind consumed an ordinal)", resp.Body)
	}
}
