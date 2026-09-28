package smsc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/metrics"
	"github.com/martialanouman/go-smsc-simulator/internal/observability"
	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
	"github.com/martialanouman/go-smsc-simulator/internal/smpptest"
	"github.com/martialanouman/go-smsc-simulator/internal/smsc"
)

// logBuffer is a concurrency-safe io.Writer: sessions log from their own goroutines while
// the test reads the buffer.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// observed is one virtual SMSC wired to a real registry and a captured DEBUG-level JSON
// log, so a test can assert what the simulator says about a session close.
type observed struct {
	name   string
	engine *smsc.Engine
	addr   string
	reg    *prometheus.Registry
	logs   *logBuffer
}

// startObserved boots cfg; tune runs between New and Serve (e.g. SetSessionTimeouts).
func startObserved(t *testing.T, cfg config.VirtualSMSCConfig, tune ...func(*smsc.Engine)) observed {
	t.Helper()

	logs := &logBuffer{}
	reg := prometheus.NewRegistry()
	engine, err := smsc.New([]config.VirtualSMSCConfig{cfg}, metrics.New(reg), observability.NewLogger(logs, slog.LevelDebug))
	if err != nil {
		t.Fatalf("smsc.New: %v", err)
	}
	for _, f := range tune {
		f(engine)
	}
	go func() { _ = engine.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := engine.Shutdown(ctx); err != nil {
			t.Errorf("engine.Shutdown: %v", err)
		}
	})
	addr, _ := engine.Addr(cfg.Name)
	return observed{
		name:   cfg.Name,
		engine: engine,
		addr:   net.JoinHostPort("127.0.0.1", strconv.Itoa(addr.(*net.TCPAddr).Port)),
		reg:    reg,
		logs:   logs,
	}
}

// series returns every sample of the named metric family, keyed by its rendered labels.
func (o observed) series(t *testing.T, family string) map[string]float64 {
	t.Helper()
	families, err := o.reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	out := map[string]float64{}
	for _, mf := range families {
		if mf.GetName() != family {
			continue
		}
		for _, m := range mf.GetMetric() {
			labels := make([]string, 0, len(m.GetLabel()))
			for _, lp := range m.GetLabel() {
				labels = append(labels, lp.GetName()+"="+lp.GetValue())
			}
			v := m.GetCounter().GetValue() + m.GetGauge().GetValue()
			out[strings.Join(labels, ",")] = v
		}
	}
	return out
}

// closeLogs returns the "session closed" records logged so far.
func (o observed) closeLogs(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(o.logs.String(), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		if rec["msg"] == "session closed" {
			out = append(out, rec)
		}
	}
	return out
}

// waitClose waits for the session close and asserts it was recorded exactly once, with the
// same reason, initiator and bind type, both on smsc_session_closed_total and in the log
// (WARN when the server initiated it, DEBUG otherwise). It returns the log record.
func (o observed) waitClose(t *testing.T, reason, initiator, bindType string) map[string]any {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for len(o.closeLogs(t)) == 0 || len(o.series(t, "smsc_session_closed_total")) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no session close recorded within 5s\nlogs:\n%s", o.logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}

	want := "bind_type=" + bindType + ",initiator=" + initiator + ",reason=" + reason + ",virtual_smsc=" + o.name
	got := o.series(t, "smsc_session_closed_total")
	if len(got) != 1 || got[want] != 1 {
		t.Errorf("smsc_session_closed_total = %v, want exactly {%s} = 1", got, want)
	}

	recs := o.closeLogs(t)
	if len(recs) != 1 {
		t.Fatalf("got %d session-closed log records, want 1:\n%s", len(recs), o.logs.String())
	}
	rec := recs[0]
	wantLevel := "DEBUG"
	if initiator == "server" {
		wantLevel = "WARN"
	}
	for k, v := range map[string]string{
		"reason": reason, "initiator": initiator, "bind_type": bindType, "virtual_smsc": o.name, "level": wantLevel,
	} {
		if rec[k] != v {
			t.Errorf("session-closed log %s = %v, want %q\nrecord: %v", k, rec[k], v, rec)
		}
	}
	return rec
}

func bound(t *testing.T, o observed) *smpptest.Client {
	t.Helper()
	c := smpptest.Dial(t, o.addr)
	c.BindTransceiver(testSystemID, testPassword)
	return c
}

func TestSessionClose_ClientUnbind(t *testing.T) {
	t.Parallel()
	o := startObserved(t, healthyConfig("close-unbind"))
	c := bound(t, o)
	c.Submit("33600000000", "33611111111", "do-not-log-me")
	c.Unbind()

	rec := o.waitClose(t, "client_unbind", "client", "transceiver")

	// The record carries what an operator needs to attribute the close, and no PDU content.
	if rec["system_id"] != testSystemID {
		t.Errorf("system_id = %v, want %q", rec["system_id"], testSystemID)
	}
	if rec["remote_addr"] != c.Conn().LocalAddr().String() {
		t.Errorf("remote_addr = %v, want %q", rec["remote_addr"], c.Conn().LocalAddr().String())
	}
	for k, want := range map[string]float64{"pdus_read": 3, "pdus_written": 3, "outbound_queue_depth": 0} {
		if rec[k] != want {
			t.Errorf("%s = %v, want %v", k, rec[k], want)
		}
	}
	for _, k := range []string{"err", "lifetime", "bind_id"} {
		if _, ok := rec[k]; !ok {
			t.Errorf("session-closed log misses %q: %v", k, rec)
		}
	}
	if strings.Contains(o.logs.String(), "do-not-log-me") {
		t.Error("PDU content leaked into the logs")
	}
}

func TestSessionClose_ClientEOF(t *testing.T) {
	t.Parallel()
	o := startObserved(t, healthyConfig("close-eof"))
	bound(t, o).Close()
	o.waitClose(t, "client_eof", "client", "transceiver")
}

// A bare TCP connect (a k8s tcpSocket probe) never binds: its close still counts, under
// bind_type="none".
func TestSessionClose_UnboundProbe(t *testing.T) {
	t.Parallel()
	o := startObserved(t, healthyConfig("close-probe"))
	smpptest.Dial(t, o.addr).Close()
	o.waitClose(t, "client_eof", "client", "none")
}

func TestSessionClose_ReadError(t *testing.T) {
	t.Parallel()
	o := startObserved(t, healthyConfig("close-reset"))
	c := bound(t, o)
	tcp := c.Conn().(*net.TCPConn)
	_ = tcp.SetLinger(0) // close with RST: the server read fails with "connection reset by peer"
	c.Close()
	o.waitClose(t, "read_error", "client", "transceiver")
}

func TestSessionClose_IdleTimeout(t *testing.T) {
	t.Parallel()
	o := startObserved(t, healthyConfig("close-idle"), func(e *smsc.Engine) {
		smsc.SetSessionTimeouts(e, 10*time.Second, 200*time.Millisecond)
	})
	bound(t, o)
	o.waitClose(t, "idle_timeout", "server", "transceiver")
}

func TestSessionClose_ProtocolError(t *testing.T) {
	t.Parallel()
	o := startObserved(t, healthyConfig("close-proto"))
	c := bound(t, o)
	if _, err := c.Conn().Write([]byte{0, 0, 0, 5}); err != nil { // command_length 5 < header
		t.Fatalf("write: %v", err)
	}
	o.waitClose(t, "protocol_error", "server", "transceiver")
}

func TestSessionClose_AuthFailed(t *testing.T) {
	t.Parallel()
	o := startObserved(t, healthyConfig("close-auth"))
	c := smpptest.Dial(t, o.addr)
	c.BindTransceiver(testSystemID, "wrong")
	o.waitClose(t, "auth_failed", "server", "transceiver")
}

func TestSessionClose_BindRejected(t *testing.T) {
	t.Parallel()
	o := startObserved(t, deadCarrierConfig("close-reject", config.DeadCarrierRejectBind))
	c := smpptest.Dial(t, o.addr)
	c.BindTransmitter(testSystemID, testPassword)
	o.waitClose(t, "bind_rejected", "server", "transmitter")
}

func TestSessionClose_FaultInjection(t *testing.T) {
	t.Parallel()
	cfg := flakyConfig("close-fault", 1)
	cfg.Scenario.Params.DisconnectIntervalTicks = pu64(1) // every submit disconnects, after its response
	o := startObserved(t, cfg)
	c := bound(t, o)
	c.SubmitAsync("33600000000", "33611111111", "m")
	o.waitClose(t, "fault_injection", "server", "transceiver")
}

func TestSessionClose_ScheduledDisconnect(t *testing.T) {
	t.Parallel()
	o := startObserved(t, disconnectConfig("close-sched", 1, config.DisconnectScopeAll, config.DisconnectAfterResponse))
	c := bound(t, o)
	c.SubmitAsync("33600000000", "33611111111", "m")
	o.waitClose(t, "scheduled_disconnect", "server", "transceiver")
}

func TestSessionClose_Shutdown(t *testing.T) {
	t.Parallel()
	o := startObserved(t, healthyConfig("close-shutdown"))
	bound(t, o)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := o.engine.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	o.waitClose(t, "shutdown", "server", "transceiver")
}

// TestSessionClose_PartialFrameTimeout reproduces the healthy-profile cut deterministically:
// with a DLR pending, the read deadline shrinks to the quiescence window, and a frame whose
// tail arrives after that window expires mid-read. The server then cuts the session.
func TestSessionClose_PartialFrameTimeout(t *testing.T) {
	t.Parallel()
	o := startObserved(t, dlrConfig("close-partial", pu64(1), 1000, config.DLROutcomeWeights{Delivered: 1}, 100))
	c := bound(t, o)
	c.Submit("33600000000", "33611111111", "m") // leaves a DLR pending for 1000 ticks

	frame, err := smpp.Encode(&smpp.PDU{CommandID: smpp.EnquireLink, SequenceNumber: 99})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := c.Conn().Write(frame[:6]); err != nil { // half a frame, the tail never comes in time
		t.Fatalf("write: %v", err)
	}
	o.waitClose(t, "partial_frame_timeout", "server", "transceiver")
}

// TestSessionClose_WriteTimeout: a client that stops reading wedges the writer until the
// write deadline fires. The server names write_timeout — not the read-side error the client's
// later hang-up produces — counts the PDUs it could no longer deliver, and leaves the
// aggregate queue-depth gauge balanced at zero.
func TestSessionClose_WriteTimeout(t *testing.T) {
	t.Parallel()
	o := startObserved(t, healthyConfig("close-wtimeout"), func(e *smsc.Engine) {
		smsc.SetSessionTimeouts(e, 200*time.Millisecond, time.Minute)
	})

	// A tiny receive buffer, set before connecting so the advertised window is small: the
	// server's writes back up within a few thousand responses.
	dialer := net.Dialer{Control: func(_, _ string, rc syscall.RawConn) error {
		return rc.Control(func(fd uintptr) {
			_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, 1024)
		})
	}}
	conn, err := dialer.Dial("tcp", o.addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	writeFrame(t, conn, &smpp.PDU{CommandID: smpp.BindTransceiver, SequenceNumber: 1,
		Body: &smpp.Bind{SystemID: testSystemID, Password: testPassword, InterfaceVersion: 0x34}})
	if frame, err := smpp.ReadPDU(conn); err != nil || len(frame) < smpp.HeaderLen {
		t.Fatalf("read bind_resp: %v", err)
	}

	submit, err := smpp.Encode(&smpp.PDU{
		CommandID: smpp.SubmitSM, SequenceNumber: 2,
		Body: &smpp.Message{SourceAddr: "33600000000", DestAddr: "33611111111", ShortMessage: []byte("m")},
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	batch := bytes.Repeat(submit, 256)
	pumped := make(chan struct{})
	go func() { // flood whole frames and never read a response; ends when conn is closed
		defer close(pumped)
		for {
			if _, err := conn.Write(batch); err != nil {
				return
			}
		}
	}()

	deadline := time.Now().Add(10 * time.Second)
	for len(o.series(t, "smsc_outbound_dropped_total")) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("writer never timed out\nlogs:\n%s", o.logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = conn.Close()
	<-pumped

	o.waitClose(t, "write_timeout", "server", "transceiver")
	if got := o.series(t, "smsc_outbound_dropped_total")["kind=resp,virtual_smsc="+o.name]; got < 1 {
		t.Errorf("smsc_outbound_dropped_total{kind=resp} = %v, want >= 1", got)
	}
	if got := o.series(t, "smsc_outbound_queue_depth")["virtual_smsc="+o.name]; got != 0 {
		t.Errorf("smsc_outbound_queue_depth = %v after close, want 0", got)
	}
}

func writeFrame(t *testing.T, conn net.Conn, pdu *smpp.PDU) {
	t.Helper()
	b, err := smpp.Encode(pdu)
	if err != nil {
		t.Fatalf("encode %s: %v", pdu.CommandID, err)
	}
	if _, err := conn.Write(b); err != nil {
		t.Fatalf("write %s: %v", pdu.CommandID, err)
	}
}
