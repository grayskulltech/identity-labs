package sim

import (
	"os"
	"testing"

	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/contract"
)

func buildWorld(t *testing.T) *World {
	t.Helper()
	wd, _ := os.Getwd()
	dir, err := DefaultToolsDir(wd)
	if err != nil {
		t.Fatal(err)
	}
	w, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestScenarios(t *testing.T) {
	w := buildWorld(t)
	for _, o := range Run(w) {
		if !o.Passed {
			t.Errorf("%s: %s", o.Scenario.Name, o.Why)
		}
	}
}

func TestEveryCallIsAudited(t *testing.T) {
	w := buildWorld(t)
	results := Run(w)
	recs := w.Audit.Records()
	// The replay scenario makes one extra call inside its Request builder.
	if len(recs) != len(results)+1 {
		t.Fatalf("expected %d audit records, got %d", len(results)+1, len(recs))
	}
	for _, r := range recs {
		if r.RequestID == "" || r.At.IsZero() {
			t.Errorf("audit record missing id or time: %+v", r)
		}
		if r.Decision == "allow" && r.Outcome != "executed" {
			t.Errorf("allowed call without executed outcome: %+v", r)
		}
		if r.Decision == "deny" && r.DenyStage == "" {
			t.Errorf("denied call without deny_stage: %+v", r)
		}
		if r.ArgsHash != "" && len(r.ArgsHash) != len("sha256:")+64 {
			t.Errorf("args_hash is not a sha256: %q", r.ArgsHash)
		}
	}
}

func TestAuditNeverCarriesArgumentValues(t *testing.T) {
	w := buildWorld(t)
	Run(w)
	for _, r := range w.Audit.Records() {
		for _, field := range []string{r.Tool, r.ArgsHash, r.DenyStage, r.Outcome, r.Subject} {
			if field == "Marcus" || field == "fw-42" {
				t.Fatalf("argument value leaked into audit: %+v", r)
			}
		}
	}
}

func TestPolicyValidateRequiresForbids(t *testing.T) {
	w := buildWorld(t)
	if err := w.Gateway.PDP.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestTierGate(t *testing.T) {
	w := buildWorld(t)
	for _, a := range []contract.Adapter{w.Gateway.PDP, w.Gateway.IdP, w.Gateway.Binding, w.Gateway.Scanner, w.Gateway.Audit, w.Gateway.Devices} {
		if a.Tier() > contract.TierOpaque {
			t.Errorf("adapter %s is tier %d in the personal deployment", a.Name(), a.Tier())
		}
	}
}
