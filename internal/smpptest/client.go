// Package smpptest is an in-process SMPP client for driving the server engine in
// tests. It reuses internal/smpp in client mode — encoding requests, decoding
// responses — so a client↔server exchange double-validates the codec, with no
// external SMPP library and no real network beyond a loopback socket (plan §13).
//
// It lives outside _test.go files so any package's tests can import it; it depends
// on testing.TB so a protocol error fails the test at its call site rather than
// being threaded back as an error.
package smpptest

import (
	"crypto/tls"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
)

// ioTimeout bounds every client read and write so a server bug surfaces as a test
// timeout instead of a hung suite.
const ioTimeout = 3 * time.Second

// Client is a minimal SMPP client over one connection. It issues requests
// synchronously — write, then read the response — which suffices while the server
// emits nothing unsolicited; asynchronous deliver_sm handling (DLR/MO) arrives with
// S4/S5.
type Client struct {
	t    testing.TB
	conn net.Conn
	seq  uint32
}

// Dial opens a connection to a virtual SMSC and registers cleanup. addr is a
// host:port the caller resolved from the engine (typically 127.0.0.1 + ephemeral).
func Dial(t testing.TB, addr string) *Client {
	t.Helper()

	conn, err := net.DialTimeout("tcp", addr, ioTimeout)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	c := &Client{t: t, conn: conn}
	t.Cleanup(c.Close)
	return c
}

// DialTLS opens a TLS connection to a virtual SMSC and registers cleanup, for exercising
// a tls-enabled listener. cfg is the client-side tls.Config: loopback tests pass
// InsecureSkipVerify (the server's self-signed cert is not in any system pool), or a
// RootCAs pool built from the server cert for stricter verification. The handshake is
// forced here (rather than lazily on first I/O) so a handshake failure fails at the call
// site, matching Dial's fail-at-dial contract.
func DialTLS(t testing.TB, addr string, cfg *tls.Config) *Client {
	t.Helper()

	dialer := &net.Dialer{Timeout: ioTimeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, cfg)
	if err != nil {
		t.Fatalf("tls dial %s: %v", addr, err)
	}
	c := &Client{t: t, conn: conn}
	t.Cleanup(c.Close)
	return c
}

// Close drops the connection. It is registered as cleanup by Dial, so tests rarely
// call it directly.
func (c *Client) Close() { _ = c.conn.Close() }

func (c *Client) nextSeq() uint32 {
	c.seq++
	return c.seq
}

// BindTransceiver binds as a transceiver and returns the response PDU.
func (c *Client) BindTransceiver(systemID, password string) *smpp.PDU {
	c.t.Helper()
	return c.roundTrip(&smpp.PDU{
		CommandID:      smpp.BindTransceiver,
		SequenceNumber: c.nextSeq(),
		Body:           &smpp.Bind{SystemID: systemID, Password: password, InterfaceVersion: 0x34},
	})
}

// BindTransmitter binds as a transmitter (may submit, may not receive deliver_sm) and
// returns the response PDU — used to exercise the DLR path on a bind with no return leg.
func (c *Client) BindTransmitter(systemID, password string) *smpp.PDU {
	c.t.Helper()
	return c.roundTrip(&smpp.PDU{
		CommandID:      smpp.BindTransmitter,
		SequenceNumber: c.nextSeq(),
		Body:           &smpp.Bind{SystemID: systemID, Password: password, InterfaceVersion: 0x34},
	})
}

// Submit sends a submit_sm with international TON/NPI and returns the response PDU.
func (c *Client) Submit(source, dest, message string) *smpp.PDU {
	c.t.Helper()
	return c.roundTrip(&smpp.PDU{
		CommandID:      smpp.SubmitSM,
		SequenceNumber: c.nextSeq(),
		Body: &smpp.Message{
			SourceAddrTON: 1, SourceAddrNPI: 1, SourceAddr: source,
			DestAddrTON: 1, DestAddrNPI: 1, DestAddr: dest,
			ShortMessage: []byte(message),
		},
	})
}

// SubmitStatus sends a submit_sm and returns just its response command_status, for
// terse assertions on the outcome (e.g. ESME_RTHROTTLED beyond a cap).
func (c *Client) SubmitStatus(source, dest, message string) smpp.CommandStatus {
	c.t.Helper()
	return c.Submit(source, dest, message).CommandStatus
}

// SubmitAsync writes a submit_sm and returns its sequence number WITHOUT reading a
// response — for tests that expect a withheld response (timeout) or a disconnect,
// where the synchronous Submit would block until the io timeout and fail the test.
func (c *Client) SubmitAsync(source, dest, message string) uint32 {
	c.t.Helper()

	seq := c.nextSeq()
	b, err := smpp.Encode(&smpp.PDU{
		CommandID:      smpp.SubmitSM,
		SequenceNumber: seq,
		Body: &smpp.Message{
			SourceAddrTON: 1, SourceAddrNPI: 1, SourceAddr: source,
			DestAddrTON: 1, DestAddrNPI: 1, DestAddr: dest,
			ShortMessage: []byte(message),
		},
	})
	if err != nil {
		c.t.Fatalf("encode submit_sm: %v", err)
	}
	if err := c.conn.SetWriteDeadline(time.Now().Add(ioTimeout)); err != nil {
		c.t.Fatalf("set write deadline: %v", err)
	}
	if _, err := c.conn.Write(b); err != nil {
		c.t.Fatalf("write submit_sm: %v", err)
	}
	return seq
}

// ExpectNoResponse asserts that no PDU arrives within d — the withheld-response
// (timeout) case. It fails if a PDU is received or the read fails for any reason other
// than the deadline.
func (c *Client) ExpectNoResponse(d time.Duration) {
	c.t.Helper()

	if err := c.conn.SetReadDeadline(time.Now().Add(d)); err != nil {
		c.t.Fatalf("set read deadline: %v", err)
	}
	frame, err := smpp.ReadPDU(c.conn)
	if err == nil {
		if pdu, derr := smpp.Decode(frame); derr == nil {
			c.t.Fatalf("expected no response, got %s", pdu.CommandID)
		}
		c.t.Fatalf("expected no response, got an undecodable frame")
	}
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		c.t.Fatalf("expected a read timeout (withheld response), got %v", err)
	}
}

// ExpectClosed asserts the server has closed the connection: the next read fails with
// something other than a timeout (EOF, reset, use-of-closed).
func (c *Client) ExpectClosed() {
	c.t.Helper()

	if err := c.conn.SetReadDeadline(time.Now().Add(ioTimeout)); err != nil {
		c.t.Fatalf("set read deadline: %v", err)
	}
	if _, err := smpp.ReadPDU(c.conn); err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			c.t.Fatalf("expected a closed connection, got a read timeout")
		}
		return // any non-timeout read error means the peer closed
	}
	c.t.Fatalf("expected a closed connection, got a PDU")
}

// ClosedWithin reports whether the server closes the connection within d: true on a
// non-timeout read error (EOF/reset), false if a PDU arrives or the read merely times out.
// Unlike ExpectClosed it never fails the test, so a caller can branch on the result — e.g. a
// scope: random scheduled disconnect that may or may not target this bind.
func (c *Client) ClosedWithin(d time.Duration) bool {
	c.t.Helper()

	if err := c.conn.SetReadDeadline(time.Now().Add(d)); err != nil {
		c.t.Fatalf("set read deadline: %v", err)
	}
	if _, err := smpp.ReadPDU(c.conn); err != nil {
		var ne net.Error
		return !errors.As(err, &ne) || !ne.Timeout() // a non-timeout error means the peer closed
	}
	return false // a PDU arrived: still open
}

// EnquireLink sends an enquire_link and returns the response PDU.
func (c *Client) EnquireLink() *smpp.PDU {
	c.t.Helper()
	return c.roundTrip(&smpp.PDU{CommandID: smpp.EnquireLink, SequenceNumber: c.nextSeq()})
}

// Unbind sends an unbind and returns the response PDU.
func (c *Client) Unbind() *smpp.PDU {
	c.t.Helper()
	return c.roundTrip(&smpp.PDU{CommandID: smpp.Unbind, SequenceNumber: c.nextSeq()})
}

// Read blocks for one unsolicited PDU from the server — a server-initiated unbind on
// shutdown, or (from S4/S5) a deliver_sm carrying a DLR or MO.
func (c *Client) Read() *smpp.PDU {
	c.t.Helper()
	return c.read()
}

// ReadWithin reads one PDU under a caller-chosen deadline, for responses that take
// longer than the default io timeout (e.g. a slow-carrier's multi-second latency).
func (c *Client) ReadWithin(d time.Duration) *smpp.PDU {
	c.t.Helper()

	if err := c.conn.SetReadDeadline(time.Now().Add(d)); err != nil {
		c.t.Fatalf("set read deadline: %v", err)
	}
	frame, err := smpp.ReadPDU(c.conn)
	if err != nil {
		c.t.Fatalf("read pdu: %v", err)
	}
	pdu, err := smpp.Decode(frame)
	if err != nil {
		c.t.Fatalf("decode pdu: %v", err)
	}
	return pdu
}

// ReadDeliverSM reads one deliver_sm (a DLR or MO) under a caller-chosen deadline and
// acknowledges it with a deliver_sm_resp, as a real ESME would. It fails the test if the
// PDU that arrives is not a deliver_sm.
func (c *Client) ReadDeliverSM(d time.Duration) *smpp.PDU {
	c.t.Helper()

	pdu := c.ReadWithin(d)
	if pdu.CommandID != smpp.DeliverSM {
		c.t.Fatalf("expected deliver_sm, got %s", pdu.CommandID)
	}
	c.write(&smpp.PDU{
		CommandID:      smpp.DeliverSMResp,
		CommandStatus:  smpp.StatusROK,
		SequenceNumber: pdu.SequenceNumber,
		Body:           &smpp.SubmitResp{}, // deliver_sm_resp: empty message_id
	})
	return pdu
}

// DrainDeliverSMs reads and acknowledges deliver_sm PDUs until d elapses with none
// arriving, returning them in receive order. Used to collect a whole flushed DLR batch
// without knowing the count in advance. It fails the test if a non-deliver_sm arrives.
func (c *Client) DrainDeliverSMs(d time.Duration) []*smpp.PDU {
	c.t.Helper()

	var out []*smpp.PDU
	for {
		if err := c.conn.SetReadDeadline(time.Now().Add(d)); err != nil {
			c.t.Fatalf("set read deadline: %v", err)
		}
		frame, err := smpp.ReadPDU(c.conn)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return out // quiet for d: the batch is fully drained
			}
			c.t.Fatalf("read pdu: %v", err)
		}
		pdu, err := smpp.Decode(frame)
		if err != nil {
			c.t.Fatalf("decode pdu: %v", err)
		}
		if pdu.CommandID != smpp.DeliverSM {
			c.t.Fatalf("expected deliver_sm, got %s", pdu.CommandID)
		}
		c.write(&smpp.PDU{
			CommandID:      smpp.DeliverSMResp,
			CommandStatus:  smpp.StatusROK,
			SequenceNumber: pdu.SequenceNumber,
			Body:           &smpp.SubmitResp{},
		})
		out = append(out, pdu)
	}
}

// CollectDeliverSMs reads until it has gathered count deliver_sm PDUs, acknowledging
// each and skipping any other PDU (e.g. an interleaved submit_sm_resp when submits and
// receipts share the stream). It fails the test if the deadline elapses first. Use it to
// assert receipts drained mid-traffic (voie a) without depending on wire interleaving.
func (c *Client) CollectDeliverSMs(count int, within time.Duration) []*smpp.PDU {
	c.t.Helper()

	deadline := time.Now().Add(within)
	var out []*smpp.PDU
	for len(out) < count {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			c.t.Fatalf("collected %d/%d deliver_sm before the deadline", len(out), count)
		}
		if err := c.conn.SetReadDeadline(time.Now().Add(remaining)); err != nil {
			c.t.Fatalf("set read deadline: %v", err)
		}
		frame, err := smpp.ReadPDU(c.conn)
		if err != nil {
			c.t.Fatalf("read pdu: %v", err)
		}
		pdu, err := smpp.Decode(frame)
		if err != nil {
			c.t.Fatalf("decode pdu: %v", err)
		}
		if pdu.CommandID != smpp.DeliverSM {
			continue // an interleaved submit_sm_resp or the like: not what we are collecting
		}
		c.write(&smpp.PDU{
			CommandID:      smpp.DeliverSMResp,
			CommandStatus:  smpp.StatusROK,
			SequenceNumber: pdu.SequenceNumber,
			Body:           &smpp.SubmitResp{},
		})
		out = append(out, pdu)
	}
	return out
}

// write encodes and sends a PDU without reading a response.
func (c *Client) write(pdu *smpp.PDU) {
	c.t.Helper()

	b, err := smpp.Encode(pdu)
	if err != nil {
		c.t.Fatalf("encode %s: %v", pdu.CommandID, err)
	}
	if err := c.conn.SetWriteDeadline(time.Now().Add(ioTimeout)); err != nil {
		c.t.Fatalf("set write deadline: %v", err)
	}
	if _, err := c.conn.Write(b); err != nil {
		c.t.Fatalf("write %s: %v", pdu.CommandID, err)
	}
}

// roundTrip writes a request and reads the single PDU the server sends back.
func (c *Client) roundTrip(req *smpp.PDU) *smpp.PDU {
	c.t.Helper()

	b, err := smpp.Encode(req)
	if err != nil {
		c.t.Fatalf("encode %s: %v", req.CommandID, err)
	}
	if err := c.conn.SetWriteDeadline(time.Now().Add(ioTimeout)); err != nil {
		c.t.Fatalf("set write deadline: %v", err)
	}
	if _, err := c.conn.Write(b); err != nil {
		c.t.Fatalf("write %s: %v", req.CommandID, err)
	}
	return c.read()
}

// read reads and decodes one PDU under the io timeout.
func (c *Client) read() *smpp.PDU {
	c.t.Helper()

	if err := c.conn.SetReadDeadline(time.Now().Add(ioTimeout)); err != nil {
		c.t.Fatalf("set read deadline: %v", err)
	}
	frame, err := smpp.ReadPDU(c.conn)
	if err != nil {
		c.t.Fatalf("read pdu: %v", err)
	}
	pdu, err := smpp.Decode(frame)
	if err != nil {
		c.t.Fatalf("decode pdu: %v", err)
	}
	return pdu
}
