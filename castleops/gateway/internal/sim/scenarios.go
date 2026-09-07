package sim

import (
	"time"

	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/contract"
	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/gateway"
)

// Scenario is one prove-out case. Setup mutates the world before the call;
// Expect is what the audit record must say.
type Scenario struct {
	Name      string
	Setup     func(w *World)
	Request   func(w *World) Request
	ExpectOK  bool
	DenyStage contract.Stage
	Policy    string // a determining policy id that must appear
	Verdict   string // guardrail verdict expected on success
}

// Scenarios is the ordered prove-out list. Each one maps to a build-order
// claim in the ADRs.
func Scenarios() []Scenario {
	guest := func(d int) map[string]any {
		return map[string]any{"guest_name": "Marcus", "duration_minutes": d, "vlan": "guest"}
	}
	return []Scenario{
		{
			Name: "gary reads the family calendar",
			Request: func(*World) Request {
				return Request{Person: "gary", Tool: "calendar.events.read", Args: map[string]any{"window": "today"}}
			},
			ExpectOK: true, Policy: "permit.family_calendar_read", Verdict: "clean",
		},
		{
			Name: "jaxon reads the adults calendar",
			Request: func(*World) Request {
				return Request{Person: "jaxon", Tool: "calendar.events.read", Args: map[string]any{"window": "today", "calendar": "adults"}}
			},
			DenyStage: contract.StagePolicy,
		},
		{
			Name: "steph provisions a 2h guest with Face ID",
			Request: func(*World) Request {
				return Request{Person: "steph", Tool: "network.guest.provision", Args: guest(120), Presence: true}
			},
			ExpectOK: true, Policy: "permit.family_guest_provision", Verdict: "clean",
		},
		{
			Name: "steph provisions a guest without Face ID",
			Request: func(*World) Request {
				return Request{Person: "steph", Tool: "network.guest.provision", Args: guest(120)}
			},
			DenyStage: contract.StagePresence, Policy: "forbid.write_requires_presence",
		},
		{
			Name: "jaxon asks for 8 hours of guest WiFi",
			Request: func(*World) Request {
				return Request{Person: "jaxon", Tool: "network.guest.provision", Args: guest(480), Presence: true}
			},
			DenyStage: contract.StageSchema,
		},
		{
			Name: "jaxon provisions a 4h guest at 15:00",
			Request: func(*World) Request {
				return Request{Person: "jaxon", Tool: "network.guest.provision", Args: guest(240), Presence: true}
			},
			ExpectOK: true, Policy: "permit.family_guest_provision",
		},
		{
			Name:  "jaxon provisions a guest at 22:30",
			Setup: func(w *World) { w.Clock.T = time.Date(2026, 9, 8, 2, 30, 0, 0, time.UTC) }, // 22:30 New York
			Request: func(*World) Request {
				return Request{Person: "jaxon", Tool: "network.guest.provision", Args: guest(60), Presence: true}
			},
			DenyStage: contract.StagePolicy, Policy: "forbid.minors_hours",
		},
		{
			Name:  "gary changes a firewall rule with no elevation",
			Setup: func(w *World) { w.Clock.T = time.Date(2026, 9, 7, 19, 0, 0, 0, time.UTC) },
			Request: func(*World) Request {
				return Request{Person: "gary", Tool: "network.firewall.rule.write", Args: map[string]any{"rule_id": "fw-42", "action": "disable"}, Presence: true}
			},
			DenyStage: contract.StagePolicy, Policy: "forbid.elevate_requires_matching_elevation",
		},
		{
			Name: "gary changes a firewall rule with an elevation for other arguments",
			Request: func(w *World) Request {
				return Request{Person: "gary", Tool: "network.firewall.rule.write",
					Args: map[string]any{"rule_id": "fw-42", "action": "disable"}, Presence: true,
					Elevation: &contract.Elevation{Tool: "network.firewall.rule.write",
						ArgsHash:      w.ArgsHash("network.firewall.rule.write", map[string]any{"rule_id": "fw-42", "action": "enable"}),
						TransactionID: "txn-1", Approver: "steph"}}
			},
			DenyStage: contract.StagePolicy, Policy: "forbid.elevate_requires_matching_elevation",
		},
		{
			Name: "gary changes a firewall rule with a matching elevation",
			Request: func(w *World) Request {
				args := map[string]any{"rule_id": "fw-42", "action": "disable"}
				return Request{Person: "gary", Tool: "network.firewall.rule.write", Args: args, Presence: true,
					Elevation: &contract.Elevation{Tool: "network.firewall.rule.write",
						ArgsHash: w.ArgsHash("network.firewall.rule.write", args), TransactionID: "txn-2", Approver: "steph"}}
			},
			ExpectOK: true, Policy: "permit.adults_firewall_with_elevation",
		},
		{
			Name: "gary reuses the same elevation a second time",
			Request: func(w *World) Request {
				args := map[string]any{"rule_id": "fw-42", "action": "disable"}
				return Request{Person: "gary", Tool: "network.firewall.rule.write", Args: args, Presence: true,
					Elevation: &contract.Elevation{Tool: "network.firewall.rule.write",
						ArgsHash: w.ArgsHash("network.firewall.rule.write", args), TransactionID: "txn-2", Approver: "steph"}}
			},
			DenyStage: contract.StagePolicy,
		},
		{
			Name: "gary approves his own elevation",
			Request: func(w *World) Request {
				args := map[string]any{"rule_id": "fw-42", "action": "disable"}
				return Request{Person: "gary", Tool: "network.firewall.rule.write", Args: args, Presence: true,
					Elevation: &contract.Elevation{Tool: "network.firewall.rule.write",
						ArgsHash: w.ArgsHash("network.firewall.rule.write", args), TransactionID: "txn-4", Approver: "gary"}}
			},
			DenyStage: contract.StagePolicy, Policy: "forbid.elevation_self_approval",
		},
		{
			Name: "jaxon changes a firewall rule even with an elevation",
			Request: func(w *World) Request {
				args := map[string]any{"rule_id": "fw-42", "action": "disable"}
				return Request{Person: "jaxon", Tool: "network.firewall.rule.write", Args: args, Presence: true,
					Elevation: &contract.Elevation{Tool: "network.firewall.rule.write",
						ArgsHash: w.ArgsHash("network.firewall.rule.write", args), TransactionID: "txn-3", Approver: "gary"}}
			},
			DenyStage: contract.StagePolicy,
		},
		{
			Name: "steph's attestation is eight days old",
			Setup: func(w *World) {
				rec, _ := w.Devices.Lookup(nil, w.People["steph"].Phone.DeviceID)
				rec.Attested.At = w.Clock.T.Add(-8 * 24 * time.Hour)
				_ = w.Devices.Upsert(rec)
			},
			Request: func(*World) Request {
				return Request{Person: "steph", Tool: "calendar.events.read", Args: map[string]any{"window": "today"}}
			},
			DenyStage: contract.StagePolicy, Policy: "forbid.posture",
		},
		{
			Name: "steph's phone reports an OS below minimum",
			Setup: func(w *World) {
				rec, _ := w.Devices.Lookup(nil, w.People["steph"].Phone.DeviceID)
				rec.Attested.At = w.Clock.T.Add(-2 * 24 * time.Hour)
				rec.Reported.OSVersion = "25.6"
				_ = w.Devices.Upsert(rec)
			},
			Request: func(*World) Request {
				return Request{Person: "steph", Tool: "calendar.events.read", Args: map[string]any{"window": "today"}}
			},
			DenyStage: contract.StagePolicy, Policy: "forbid.posture",
		},
		{
			Name: "steph's phone is revoked after being lost",
			Setup: func(w *World) {
				rec, _ := w.Devices.Lookup(nil, w.People["steph"].Phone.DeviceID)
				rec.Reported.OSVersion = "26.0.1"
				_ = w.Devices.Upsert(rec)
				_ = w.Devices.Revoke(nil, w.People["steph"].Phone.DeviceID, "lost phone")
			},
			Request: func(*World) Request {
				return Request{Person: "steph", Tool: "calendar.events.read", Args: map[string]any{"window": "today"}}
			},
			DenyStage: contract.StageDevice,
		},
		{
			Name: "gary's token is bound to a key his phone does not hold",
			Request: func(w *World) Request {
				return Request{Person: "gary", Tool: "calendar.events.read", Args: map[string]any{"window": "today"}, BindTokenTo: mustKey()}
			},
			DenyStage: contract.StageAttestation,
		},
		{
			Name: "a proof signed by an unenrolled key",
			Request: func(w *World) Request {
				return Request{Person: "gary", Tool: "calendar.events.read", Args: map[string]any{"window": "today"}, SignProofWith: mustKey()}
			},
			DenyStage: contract.StageAttestation,
		},
		{
			Name: "a replayed proof",
			Request: func(w *World) Request {
				_, proof := w.Call(Request{Person: "gary", Tool: "calendar.events.read", Args: map[string]any{"window": "today"}})
				return Request{Person: "gary", Tool: "calendar.events.read", Args: map[string]any{"window": "today"}, ReuseProof: proof}
			},
			DenyStage: contract.StageAttestation,
		},
		{
			Name: "a poisoned calendar title reaches the phone as inert text",
			Setup: func(w *World) {
				w.Gateway.Workers["castleops-calendar"] = PoisonWorker{}
			},
			Request: func(*World) Request {
				return Request{Person: "gary", Tool: "calendar.events.read", Args: map[string]any{"window": "today"}}
			},
			ExpectOK: true, Verdict: "blocked",
		},
	}
}

// Outcome is a scenario's observed result.
type Outcome struct {
	Scenario Scenario
	Out      gateway.CallOutput
	Passed   bool
	Why      string
}

// Run executes every scenario against a fresh world and reports outcomes.
func Run(w *World) []Outcome {
	var results []Outcome
	for _, sc := range Scenarios() {
		if sc.Setup != nil {
			sc.Setup(w)
		}
		out, _ := w.Call(sc.Request(w))
		o := Outcome{Scenario: sc, Out: out, Passed: true}
		switch {
		case sc.ExpectOK && out.IsError:
			o.Passed, o.Why = false, "expected success, got deny at "+string(out.DenyStage)+": "+out.Content
		case !sc.ExpectOK && !out.IsError:
			o.Passed, o.Why = false, "expected deny, got success"
		case !sc.ExpectOK && out.DenyStage != sc.DenyStage:
			o.Passed, o.Why = false, "expected deny at "+string(sc.DenyStage)+", got "+string(out.DenyStage)
		case sc.Policy != "" && !contains(out.Decision.DeterminingPolicies, sc.Policy):
			o.Passed, o.Why = false, "expected policy "+sc.Policy+" among "+join(out.Decision.DeterminingPolicies)
		case sc.Verdict != "" && out.Verdict != sc.Verdict:
			o.Passed, o.Why = false, "expected guardrail "+sc.Verdict+", got "+out.Verdict
		}
		results = append(results, o)
	}
	return results
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func join(list []string) string {
	out := ""
	for i, x := range list {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	if out == "" {
		return "(none)"
	}
	return out
}
