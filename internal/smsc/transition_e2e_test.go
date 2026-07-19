package smsc_test

import (
	"testing"
	"time"

	"github.com/martialanouman/go-smsc-simulator/internal/config"
	"github.com/martialanouman/go-smsc-simulator/internal/observability"
	"github.com/martialanouman/go-smsc-simulator/internal/smpp"
	"github.com/martialanouman/go-smsc-simulator/internal/smpptest"
)

// transitionConfig builds a seeded healthy virtual SMSC that transitions healthy ->
// dead-carrier at deadTick and back to healthy at healthyTick.
func transitionConfig(name string, seed, deadTick, healthyTick uint64) config.VirtualSMSCConfig {
	cfg := baseConfig(name, pu64(seed), config.ScenarioConfig{
		Profile: config.ProfileHealthy,
		Latency: config.LatencyConfig{Distribution: config.LatencyFixed, Params: config.LatencyParams{MS: pu64(0)}},
	})
	cfg.ScheduledTransitions = []config.ScheduledTransition{
		{AtTick: deadTick, ToProfile: config.ProfileDeadCarrier},
		{AtTick: healthyTick, ToProfile: config.ProfileHealthy},
	}
	return cfg
}

func activeProfile(t *testing.T, baseURL, name string) string {
	t.Helper()
	var view observability.VirtualSMSCView
	decodeGET(t, baseURL+"/v1/virtual-smscs/"+name, &view)
	return view.ActiveProfile
}

// TestE2E_ScheduledTransition_HealthyDeadHealthy is T3, the reference case: healthy up to
// deadTick, dead-carrier (submits time out) from deadTick, healthy again from healthyTick.
// The submit AT at_tick already runs under the new profile — the transition is keyed to the
// logical clock, applied before evaluation.
func TestE2E_ScheduledTransition_HealthyDeadHealthy(t *testing.T) {
	t.Parallel()

	const (
		deadTick    = 2
		healthyTick = 4
		name        = "carrier-transition"
	)
	h := startWith(t, transitionConfig(name, 1, deadTick, healthyTick))
	client := smpptest.Dial(t, h.smppAddr)
	client.BindTransceiver(testSystemID, testPassword)

	if got := activeProfile(t, h.baseURL, name); got != "healthy" {
		t.Fatalf("active_profile before any submit = %q, want healthy", got)
	}

	// tick 1: still healthy.
	if resp := client.Submit("33600000000", "33611111111", "m"); resp.CommandStatus != smpp.StatusROK {
		t.Fatalf("submit tick 1 = %d, want ROK (healthy)", resp.CommandStatus)
	}
	// tick 2 (deadTick) and tick 3: dead-carrier times out every submit — no response.
	client.SubmitAsync("33600000000", "33611111111", "m")
	client.ExpectNoResponse(300 * time.Millisecond)
	client.SubmitAsync("33600000000", "33611111111", "m")
	client.ExpectNoResponse(300 * time.Millisecond)

	if got := activeProfile(t, h.baseURL, name); got != "dead-carrier" {
		t.Fatalf("active_profile during dead range = %q, want dead-carrier", got)
	}

	// tick 4 (healthyTick) and tick 5: healthy again — responses resume.
	if resp := client.Submit("33600000000", "33611111111", "m"); resp.CommandStatus != smpp.StatusROK {
		t.Fatalf("submit tick 4 = %d, want ROK (healthy again)", resp.CommandStatus)
	}
	if resp := client.Submit("33600000000", "33611111111", "m"); resp.CommandStatus != smpp.StatusROK {
		t.Fatalf("submit tick 5 = %d, want ROK (healthy)", resp.CommandStatus)
	}
	if got := activeProfile(t, h.baseURL, name); got != "healthy" {
		t.Fatalf("active_profile after transition back = %q, want healthy", got)
	}
}
