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
