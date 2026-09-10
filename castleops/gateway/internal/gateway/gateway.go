// Package gateway is the decision pipeline. Every tool call passes through
// the same ordered stages; the first failing stage denies, and every call,
// allowed or denied, produces exactly one audit record.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/contract"
	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/guardrail"
	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/registry"
)

// Worker executes a tool as a bounded NHI. The reference workers are test
// doubles; real ones speak to UniFi, CalDAV, and Home Assistant.
type Worker interface {
	Execute(ctx context.Context, tool contract.Tool, args map[string]any, principal contract.PrincipalAssertion) (string, error)
}

// Gateway wires the adapters. Construct with New so tier checks run.
type Gateway struct {
	Tools    *registry.ToolRegistry
	Devices  contract.DevicePosture
	IdP      contract.IdentityProvider
	PDP      contract.PolicyDecisionPoint
	Binding  contract.DeviceBinding
	Scanner  contract.GuardrailScanner
	Audit    contract.AuditSink
	Workers  map[string]Worker
	Policy   contract.PolicyParams
	MaxTier  contract.Tier
	Now      func() time.Time
	Endpoint string // canonical URL clients bind DPoP proofs to

	usedElevations *sync.Map // transaction id -> unix time consumed
}

// New validates adapters against the deployment's tier allowance and the
// policy set against its required ids. Either failure is fatal by design.
func New(g Gateway) (*Gateway, error) {
	if g.MaxTier == 0 {
		g.MaxTier = contract.TierOpaque
	}
	if g.Now == nil {
		g.Now = time.Now
	}
	g.usedElevations = &sync.Map{}
	for _, a := range []contract.Adapter{g.Devices, g.IdP, g.PDP, g.Binding, g.Scanner, g.Audit} {
		if a == nil {
			return nil, errors.New("gateway: every adapter must be set")
		}
		if a.Tier() > g.MaxTier {
			return nil, fmt.Errorf("gateway: adapter %s is tier %d, deployment allows up to %d", a.Name(), a.Tier(), g.MaxTier)
		}
	}
	if err := g.PDP.Validate(); err != nil {
		return nil, fmt.Errorf("gateway: policy: %w", err)
	}
	if g.Policy.MinimumOS == "" || g.Policy.HouseholdTimeZone == "" || len(g.Policy.AllowedModels) == 0 {
		return nil, errors.New("gateway: policy params need minimum_os, household_time_zone, allowed_models")
	}
	return &g, nil
}

// CallInput is one tools/call after transport has been terminated.
type CallInput struct {
	RequestID           string
	PeerCertFingerprint string // sha256:... of the client certificate presented over mTLS
	AccessToken         string
	Proof               string // DPoP proof or App Attest assertion
	Method              string
	URL                 string
	ToolID              string
	Args                map[string]any
}

// CallOutput is what the MCP layer renders back to the phone.
type CallOutput struct {
	Content   string
	IsError   bool
	DenyStage contract.Stage
	Decision  contract.AuthzDecision
	Verdict   string
	Audit     contract.AuditRecord
}

// Call runs the pipeline. It never panics on adapter errors and always writes
// one audit record before returning.
func (g *Gateway) Call(ctx context.Context, in CallInput) CallOutput {
	now := g.Now()
	rec := contract.AuditRecord{
		RequestID: in.RequestID, At: now, Tool: in.ToolID,
		ActorChain: []string{}, DeterminingPolicies: []string{}, Decision: "deny", Outcome: "denied",
	}
	out := CallOutput{IsError: true}

	finish := func(stage contract.Stage, msg string) CallOutput {
		rec.DenyStage = string(stage)
		out.DenyStage = stage
		out.Content = msg
		out.Audit = rec
		_ = g.Audit.Write(ctx, rec)
		return out
	}

	// 1. Transport: the peer certificate names an enrolled device.
	if in.PeerCertFingerprint == "" {
		return finish(contract.StageTransport, "no device certificate presented")
	}
	device, err := g.Devices.Lookup(ctx, in.PeerCertFingerprint)
	if err != nil {
		return finish(stageOf(err, contract.StageDevice), safeMsg(err))
	}
	rec.DeviceID = device.DeviceID

	// 2. Token: who is asking.
	principal, err := g.IdP.VerifyAccessToken(ctx, in.AccessToken)
	if err != nil {
		return finish(stageOf(err, contract.StageToken), safeMsg(err))
	}
	rec.Subject = principal.Subject
	rec.ActorChain = principal.ActorChain

	// 3. Device: the record is active and belongs to this subject.
	if device.Status != "active" {
		return finish(contract.StageDevice, "device is "+device.Status)
	}
	if device.Subject != principal.Subject {
		return finish(contract.StageDevice, "device is not enrolled to this person")
	}

	// 4. Attestation: this request was signed, now, by an enrolled key.
	binding, err := g.Binding.Verify(ctx, contract.BindingInput{
		Method: in.Method, URL: in.URL, AccessToken: in.AccessToken, Proof: in.Proof, Device: device, Now: now,
	})
	if err != nil {
		return finish(stageOf(err, contract.StageAttestation), safeMsg(err))
	}
	if principal.Confirmation == nil {
		return finish(contract.StageToken, "token is not sender-constrained")
	}
	if !binding.Presence && binding.KeyThumbprint != principal.Confirmation.JKT {
		return finish(contract.StageAttestation, "token is bound to a different key")
	}
	rec.PresenceVerified = binding.Presence

	// 5. Schema: the tool exists and the arguments are exactly what it accepts.
	tool, ok := g.Tools.Get(in.ToolID)
	if !ok {
		return finish(contract.StageSchema, "unknown tool")
	}
	rec.ToolVersion = tool.Version
	args, err := g.Tools.Normalize(tool.ID, in.Args)
	if err != nil {
		return finish(contract.StageSchema, "arguments rejected")
	}
	argsHash := registry.ArgsHash(args)
	rec.ArgsHash = argsHash

	// 6. Policy: Cedar decides with only the hinted arguments in view.
	hinted := map[string]any{}
	for name := range tool.PolicyHints {
		if v, ok := args[name]; ok {
			hinted[name] = v
		}
	}
	decision, err := g.PDP.Decide(ctx, contract.AuthzRequest{
		RequestID: in.RequestID, Now: now, Principal: principal,
		Tool: contract.ToolRef{ID: tool.ID, Version: tool.Version, Kind: tool.Kind, Domain: tool.Domain},
		Args: hinted, ArgsHash: argsHash, Device: device, PresenceVerified: binding.Presence, Policy: g.Policy,
	})
	if err != nil {
		return finish(contract.StagePolicy, "policy unavailable")
	}
	out.Decision = decision
	rec.DeterminingPolicies = decision.DeterminingPolicies
	if !decision.Allowed() {
		stage := contract.StagePolicy
		for _, id := range decision.DeterminingPolicies {
			if id == "forbid.write_requires_presence" {
				stage = contract.StagePresence
			}
		}
		if principal.Elevation != nil {
			rec.ElevationTransactionID = principal.Elevation.TransactionID
		}
		return finish(stage, decision.Reason)
	}
	if principal.Elevation != nil {
		// Single use. The token dies the moment it succeeds, whatever its exp says.
		rec.ElevationTransactionID = principal.Elevation.TransactionID
		if _, used := g.usedElevations.LoadOrStore(principal.Elevation.TransactionID, now.Unix()); used {
			return finish(contract.StagePolicy, "this elevation has already been used")
		}
	}
	rec.Decision = "allow"

	// 7. Execute as the worker NHI for this tool.
	worker, ok := g.Workers[tool.Worker]
	if !ok {
		rec.Outcome = "failed"
		return finish(contract.StageExecution, "no worker for "+tool.Worker)
	}
	result, err := worker.Execute(ctx, tool, args, principal)
	if err != nil {
		rec.Outcome = "failed"
		return finish(contract.StageExecution, "the action failed")
	}
	if len(result) > tool.Result.MaxBytes {
		// Results are compact by contract. Oversize output is a worker bug;
		// truncate and say so rather than ship it.
		result = result[:tool.Result.MaxBytes] + " [truncated: result exceeded tool max_bytes]"
	}

	// 8. Guardrail: the only untrusted text in the system is scanned here.
	verdict, err := g.Scanner.ScanResult(ctx, tool.ID, result)
	if err != nil {
		rec.Outcome = "failed"
		return finish(contract.StageGuardrail, "guardrail unavailable")
	}
	rec.GuardrailVerdict = verdict.Verdict
	out.Verdict = verdict.Verdict
	if verdict.Verdict == "blocked" {
		result = guardrail.BlockedNotice
	}

	rec.Outcome = "executed"
	out.IsError = false
	out.Content = result
	out.Audit = rec
	_ = g.Audit.Write(ctx, rec)
	return out
}

func stageOf(err error, fallback contract.Stage) contract.Stage {
	var se *contract.StageError
	if errors.As(err, &se) {
		return se.Stage
	}
	return fallback
}

// safeMsg returns a message safe to show the requester. Stage errors are
// written to be safe; anything else is replaced.
func safeMsg(err error) string {
	var se *contract.StageError
	if errors.As(err, &se) {
		return se.Msg
	}
	return "request denied"
}
