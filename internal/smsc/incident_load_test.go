//go:build loadtest

package smsc_test

import (
	"bufio"
	"net"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
)

// TestLoad_HealthySessionCloses reproduces the step-280 incident shape: N transceiver binds
// on a healthy carrier (fixed 5 ms, DLR 5 ticks later), each pipelining a window of 32
// submit_sm and reading its deliver_sm slowly. A healthy carrier never disconnects on its
// own, so every server-initiated close is named here, with its reason, for investigation.
// CPU-bound goroutines stand in for the co-resident load of the k3s node: they get the
// session goroutines preempted between syscalls, as a saturated node does. Clients write
// through a buffered writer, so frames straddle TCP segments as a real ESME's do.
func TestLoad_HealthySessionCloses(t *testing.T) {
	const (
		binds    = 52
		window   = 32
		slowRead = 2 * time.Millisecond // per deliver_sm, before its deliver_sm_resp
		runFor   = 20 * time.Second
	)
	cfg := dlrConfig("incident", pu64(42), 5, config.DLROutcomeWeights{Delivered: 1}, config.QuiescenceFlushDefaultMs)
	cfg.Scenario.Latency.Params.MS = pu64(5)
	o := startObserved(t, cfg)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < runtime.GOMAXPROCS(0); i++ {
		wg.Add(1)
		go func() { // co-resident CPU burner
			defer wg.Done()
			for x := 0; ; x++ {
				if x%1_000_000 == 0 {
					select {
					case <-stop:
						return
					default:
					}
				}
			}
		}()
	}
	for i := 0; i < binds; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pipelinedClient(t, o.addr, window, slowRead, stop)
		}()
	}
	time.Sleep(runFor)
	close(stop)
	wg.Wait()
	time.Sleep(500 * time.Millisecond) // let server teardowns record

	var lines []string
	for labels, n := range o.series(t, "smsc_session_closed_total") {
		lines = append(lines, labels+" = "+strconv.FormatFloat(n, 'f', -1, 64))
	}
	sort.Strings(lines)
	t.Logf("smsc_session_closed_total:\n  %s", strings.Join(lines, "\n  "))
	for _, rec := range o.closeLogs(t) {
		if rec["initiator"] == "server" {
			t.Logf("server close: reason=%v err=%v lifetime=%v pdus_read=%v queue=%v pending=%v",
				rec["reason"], rec["err"], rec["lifetime"], rec["pdus_read"], rec["outbound_queue_depth"], rec["pending_scheduled"])
		}
	}
	for labels := range o.series(t, "smsc_session_closed_total") {
		if strings.Contains(labels, "initiator=server") {
			t.Errorf("healthy carrier closed a session on its own: %s", labels)
		}
	}
}

// pipelinedClient binds, keeps up to window submit_sm in flight, and answers each
// deliver_sm after slowRead. It stops on stop or when the server closes the link.
func pipelinedClient(t *testing.T, addr string, window int, slowRead time.Duration, stop <-chan struct{}) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Errorf("dial: %v", err)
		return
	}
	defer func() { _ = conn.Close() }()

	var wmu sync.Mutex
	bw := bufio.NewWriterSize(conn, 4096) // frames straddle flush boundaries, as a real ESME's do
	send := func(p *smpp.PDU, flush bool) error {
		b, err := smpp.Encode(p)
		if err != nil {
			return err
		}
		wmu.Lock()
		defer wmu.Unlock()
		if _, err = bw.Write(b); err == nil && flush {
			err = bw.Flush()
		}
		return err
	}
	write := func(p *smpp.PDU) error { return send(p, true) }
	if err := write(&smpp.PDU{CommandID: smpp.BindTransceiver, SequenceNumber: 1,
		Body: &smpp.Bind{SystemID: testSystemID, Password: testPassword, InterfaceVersion: 0x34}}); err != nil {
		t.Errorf("bind: %v", err)
		return
	}
	if _, err := smpp.ReadPDU(conn); err != nil {
		t.Errorf("bind_resp: %v", err)
		return
	}

	slots := make(chan struct{}, window)
	done := make(chan struct{})
	go func() { // reader: frees a slot per submit_sm_resp, acks deliver_sm slowly
		defer close(done)
		for {
			frame, err := smpp.ReadPDU(conn)
			if err != nil {
				return
			}
			pdu, err := smpp.Decode(frame)
			if err != nil {
				return
			}
			switch pdu.CommandID {
			case smpp.SubmitSMResp:
				<-slots
			case smpp.DeliverSM:
				time.Sleep(slowRead)
				if write(&smpp.PDU{CommandID: smpp.DeliverSMResp, SequenceNumber: pdu.SequenceNumber,
					Body: &smpp.SubmitResp{}}) != nil {
					return
				}
			}
		}
	}()

	for seq := uint32(2); ; seq++ {
		select {
		case <-stop:
			_ = write(&smpp.PDU{CommandID: smpp.Unbind, SequenceNumber: seq})
			time.Sleep(50 * time.Millisecond)
			return
		case <-done:
			return // the server closed the link
		case slots <- struct{}{}:
		}
		// Flush only when the window is full: a pipelined burst leaves in 4 KiB writes.
		if send(&smpp.PDU{CommandID: smpp.SubmitSM, SequenceNumber: seq,
			Body: &smpp.Message{SourceAddr: "33600000000", DestAddr: "33611111111", ShortMessage: []byte("load")}},
			len(slots) == window) != nil {
			return
		}
	}
}
