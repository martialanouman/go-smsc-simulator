package observability_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/martialanouman/go-smsc-simulator/internal/observability"
)

// fakeInspector is a hand-written stand-in for the SMPP engine (test strategy §7:
// prefer a fake over a mock). "carrier-a" is the only known virtual SMSC.
type fakeInspector struct{}

func (fakeInspector) VirtualSMSCs() []observability.VirtualSMSCView {
	return []observability.VirtualSMSCView{{Name: "carrier-a", Port: 2775, ActiveProfile: "healthy", LogicalClock: 7}}
}

func (fakeInspector) VirtualSMSC(id string) (observability.VirtualSMSCView, bool) {
	if id != "carrier-a" {
		return observability.VirtualSMSCView{}, false
	}
	return observability.VirtualSMSCView{Name: "carrier-a", ActiveProfile: "healthy", LogicalClock: 7}, true
}

func (fakeInspector) ReceivedPDUs(id string, _ observability.PDUFilter) ([]observability.RecordedPDUView, bool) {
	if id != "carrier-a" {
		return nil, false
	}
	return []observability.RecordedPDUView{{Index: 0, MessageID: "1-0001", SourceAddr: "33600", DestAddr: "33611"}}, true
}

func (fakeInspector) Binds(id string) ([]observability.BindView, bool) {
	if id != "carrier-a" {
		return nil, false
	}
	return []observability.BindView{{ID: 1, SystemID: "smppclient1", BindType: "transceiver"}}, true
}

func (fakeInspector) LogicalClock(id string) (uint64, bool) {
	if id != "carrier-a" {
		return 0, false
	}
	return 7, true
}

// TestInspection_KnownVirtualSMSC checks each /v1 read returns 200 with the
// expected shape for a known id.
func TestInspection_KnownVirtualSMSC(t *testing.T) {
	t.Parallel()

	base := startServer(t, fakeInspector{})

	t.Run("list", func(t *testing.T) {
		t.Parallel()
		var got []observability.VirtualSMSCView
		decodeGET(t, base+"/v1/virtual-smscs", &got)
		if len(got) != 1 || got[0].Name != "carrier-a" {
			t.Fatalf("list = %+v, want one carrier-a", got)
		}
	})

	t.Run("logical-clock", func(t *testing.T) {
		t.Parallel()
		var got map[string]uint64
		decodeGET(t, base+"/v1/virtual-smscs/carrier-a/logical-clock", &got)
		if got["logical_clock"] != 7 {
			t.Fatalf("logical_clock = %d, want 7", got["logical_clock"])
		}
	})

	t.Run("received-pdus", func(t *testing.T) {
		t.Parallel()
		var got []observability.RecordedPDUView
		decodeGET(t, base+"/v1/virtual-smscs/carrier-a/received-pdus", &got)
		if len(got) != 1 || got[0].MessageID != "1-0001" {
			t.Fatalf("received-pdus = %+v, want one 1-0001", got)
		}
	})

	t.Run("binds", func(t *testing.T) {
		t.Parallel()
		var got []observability.BindView
		decodeGET(t, base+"/v1/virtual-smscs/carrier-a/binds", &got)
		if len(got) != 1 || got[0].BindType != "transceiver" {
			t.Fatalf("binds = %+v, want one transceiver", got)
		}
	})
}

// TestInspection_UnknownVirtualSMSC checks that an unknown id is 404 on every
// per-SMSC route.
func TestInspection_UnknownVirtualSMSC(t *testing.T) {
	t.Parallel()

	base := startServer(t, fakeInspector{})
	for _, path := range []string{
		"/v1/virtual-smscs/ghost",
		"/v1/virtual-smscs/ghost/received-pdus",
		"/v1/virtual-smscs/ghost/binds",
		"/v1/virtual-smscs/ghost/logical-clock",
	} {
		resp := get(t, base+path)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", path, resp.StatusCode)
		}
	}
}

// TestInspection_RejectsMutatingVerbs extends invariant (c) to the inspection
// routes: a mutating verb on a /v1 endpoint must be refused with 405, structurally.
func TestInspection_RejectsMutatingVerbs(t *testing.T) {
	t.Parallel()

	base := startServer(t, fakeInspector{})
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req, err := http.NewRequestWithContext(context.Background(), method, base+"/v1/virtual-smscs", nil)
		if err != nil {
			t.Fatalf("build %s: %v", method, err)
		}
		resp, err := testClient.Do(req)
		if err != nil {
			t.Fatalf("%s /v1/virtual-smscs: %v", method, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s /v1/virtual-smscs status = %d, want 405", method, resp.StatusCode)
		}
	}
}

// TestInspection_DisabledWhenNilInspector confirms the /v1 routes are absent when
// the surface is built without an inspector (black-box / pre-engine boot).
func TestInspection_DisabledWhenNilInspector(t *testing.T) {
	t.Parallel()

	resp := get(t, startServer(t, nil)+"/v1/virtual-smscs")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /v1/virtual-smscs with nil inspector = %d, want 404", resp.StatusCode)
	}
}

func decodeGET(t *testing.T, url string, dst any) {
	t.Helper()
	resp := get(t, url)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}
