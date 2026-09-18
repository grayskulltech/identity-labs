// Package cedar is the embedded PolicyDecisionPoint adapter (ADR-001). It
// evaluates the CastleOps Cedar policy set in-process with no network call.
package cedar

import (
	"context"
	_ "embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	cedar "github.com/cedar-policy/cedar-go"
	"github.com/cedar-policy/cedar-go/types"

	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/contract"
)

//go:embed castleops.cedar
var defaultPolicies []byte

// DefaultPolicies returns the embedded v1 policy source.
func DefaultPolicies() []byte { return defaultPolicies }

// requiredPolicyIDs must all be present or Validate fails. They are the
// forbids the design depends on; losing one silently is the failure we guard.
var requiredPolicyIDs = []string{
	"forbid.posture",
	"forbid.write_requires_presence",
	"forbid.elevate_requires_matching_elevation",
	"forbid.minors_hours",
	"forbid.elevation_self_approval",
}

// PDP is the Cedar policy decision point.
type PDP struct {
	set *cedar.PolicySet
}

// New parses the policy source and re-keys each policy by its @id
// annotation, so decisions report stable ids rather than positional ones.
func New(source []byte) (*PDP, error) {
	parsed, err := cedar.NewPolicySetFromBytes("castleops.cedar", source)
	if err != nil {
		return nil, fmt.Errorf("parse policies: %w", err)
	}
	keyed := cedar.NewPolicySet()
	for posID, p := range parsed.All() {
		id, ok := p.Annotations()["id"]
		if !ok || id == "" {
			return nil, fmt.Errorf("policy %s has no @id annotation", posID)
		}
		if !keyed.Add(cedar.PolicyID(id), p) {
			return nil, fmt.Errorf("duplicate policy id %q", id)
		}
	}
	return &PDP{set: keyed}, nil
}

func (p *PDP) Name() string        { return "cedar-embedded" }
func (p *PDP) Tier() contract.Tier { return contract.TierLAN }

// Validate confirms the required forbids exist. Called at startup; fatal on error.
func (p *PDP) Validate() error {
	for _, id := range requiredPolicyIDs {
		if p.set.Get(cedar.PolicyID(id)) == nil {
			return fmt.Errorf("required policy %q is missing", id)
		}
	}
	return nil
}

// PolicyIDs lists the loaded policy ids, sorted.
func (p *PDP) PolicyIDs() []string {
	var ids []string
	for id := range p.set.All() {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	return ids
}

// Decide evaluates one request. Any evaluation error yields deny, because a
// forbid that errored is a forbid that did not fire.
func (p *PDP) Decide(_ context.Context, req contract.AuthzRequest) (contract.AuthzDecision, error) {
	entities, cedarReq, err := build(req)
	if err != nil {
		return contract.AuthzDecision{
			RequestID: req.RequestID, Decision: "deny", Errors: []string{err.Error()},
			DeterminingPolicies: []string{}, Reason: "policy input could not be built",
		}, nil
	}
	decision, diag := p.set.IsAuthorized(entities, cedarReq)

	out := contract.AuthzDecision{RequestID: req.RequestID, DeterminingPolicies: []string{}, Errors: []string{}}
	for _, r := range diag.Reasons {
		out.DeterminingPolicies = append(out.DeterminingPolicies, string(r.PolicyID))
	}
	sort.Strings(out.DeterminingPolicies)
	for _, e := range diag.Errors {
		out.Errors = append(out.Errors, fmt.Sprintf("%s: %s", e.PolicyID, e.Message))
	}
	if decision == types.Allow && len(out.Errors) == 0 {
		out.Decision = "allow"
		return out, nil
	}
	out.Decision = "deny"
	out.Reason = reasonFor(out.DeterminingPolicies, out.Errors)
	return out, nil
}

func reasonFor(policies, errs []string) string {
	if len(errs) > 0 {
		return "policy evaluation error"
	}
	for _, id := range policies {
		switch id {
		case "forbid.posture":
			return "device posture does not meet the household minimum"
		case "forbid.write_requires_presence":
			return "this change needs Face ID on the phone"
		case "forbid.elevate_requires_matching_elevation":
			return "this action needs an approved elevation for exactly this request"
		case "forbid.minors_hours":
			return "outside allowed hours"
		case "forbid.elevation_self_approval":
			return "an elevation must be approved by someone else"
		}
	}
	return "no policy permits this action"
}

// build turns an AuthzRequest into Cedar entities and a Cedar request.
func build(req contract.AuthzRequest) (types.EntityMap, types.Request, error) {
	loc, err := time.LoadLocation(req.Policy.HouseholdTimeZone)
	if err != nil {
		return nil, types.Request{}, fmt.Errorf("household_time_zone: %w", err)
	}
	if req.Policy.MinimumOS == "" {
		return nil, types.Request{}, fmt.Errorf("policy.minimum_os is required")
	}

	entities := types.EntityMap{}

	// Principal and groups. Parents are listed completely so `in` needs no
	// transitive closure over the group graph.
	user := types.NewEntityUID("User", types.String(req.Principal.Subject))
	groupUIDs := make([]types.EntityUID, 0, len(req.Principal.Groups))
	for _, g := range req.Principal.Groups {
		uid := types.NewEntityUID("Group", types.String(g))
		groupUIDs = append(groupUIDs, uid)
		entities[uid] = types.Entity{UID: uid, Parents: types.NewEntityUIDSet(), Attributes: types.NewRecord(types.RecordMap{}), Tags: types.NewRecord(types.RecordMap{})}
	}
	entities[user] = types.Entity{
		UID:        user,
		Parents:    types.NewEntityUIDSet(groupUIDs...),
		Attributes: types.NewRecord(types.RecordMap{"subject": types.String(req.Principal.Subject)}),
		Tags:       types.NewRecord(types.RecordMap{}),
	}

	// Action and its groups: kind, domain, and every dotted prefix of the id.
	action := types.NewEntityUID("Action", types.String(req.Tool.ID))
	var actionParents []types.EntityUID
	for _, g := range actionGroups(req.Tool) {
		uid := types.NewEntityUID("Action", types.String("group:"+g))
		actionParents = append(actionParents, uid)
		entities[uid] = types.Entity{UID: uid, Parents: types.NewEntityUIDSet(), Attributes: types.NewRecord(types.RecordMap{}), Tags: types.NewRecord(types.RecordMap{})}
	}
	entities[action] = types.Entity{
		UID:        action,
		Parents:    types.NewEntityUIDSet(actionParents...),
		Attributes: types.NewRecord(types.RecordMap{}),
		Tags:       types.NewRecord(types.RecordMap{}),
	}

	// Resource: the tool, with the attributes policies read.
	tool := types.NewEntityUID("Tool", types.String(req.Tool.ID))
	entities[tool] = types.Entity{
		UID:     tool,
		Parents: types.NewEntityUIDSet(),
		Attributes: types.NewRecord(types.RecordMap{
			"toolId":  types.String(req.Tool.ID),
			"kind":    types.String(string(req.Tool.Kind)),
			"domain":  types.String(req.Tool.Domain),
			"version": types.Long(req.Tool.Version),
		}),
		Tags: types.NewRecord(types.RecordMap{}),
	}

	ctx, err := buildContext(req, loc)
	if err != nil {
		return nil, types.Request{}, err
	}
	return entities, types.Request{Principal: user, Action: action, Resource: tool, Context: ctx}, nil
}

func actionGroups(t contract.ToolRef) []string {
	groups := []string{string(t.Kind), t.Domain}
	parts := strings.Split(t.ID, ".")
	for i := 1; i < len(parts); i++ {
		groups = append(groups, strings.Join(parts[:i], "."))
	}
	return groups
}

// buildContext produces the context record. Every attribute any policy
// references is always present; absent facts are represented explicitly.
func buildContext(req contract.AuthzRequest, loc *time.Location) (types.Record, error) {
	args := types.RecordMap{}
	for name, v := range req.Args {
		cv, err := toCedarValue(v)
		if err != nil {
			return types.Record{}, fmt.Errorf("args.%s: %w", name, err)
		}
		args[types.String(name)] = cv
	}

	dev := req.Device
	attestOK, err := versionAtLeast(dev.Attested.OSVersion, req.Policy.MinimumOS)
	if err != nil {
		return types.Record{}, fmt.Errorf("attested os_version: %w", err)
	}
	reportOK, err := versionAtLeast(dev.Reported.OSVersion, req.Policy.MinimumOS)
	if err != nil {
		return types.Record{}, fmt.Errorf("reported os_version: %w", err)
	}
	modelAllowed := false
	for _, m := range req.Policy.AllowedModels {
		if m == dev.Model {
			modelAllowed = true
			break
		}
	}

	elev := types.RecordMap{
		"present":  types.Boolean(false),
		"tool":     types.String(""),
		"argsHash": types.String(""),
		"approver": types.String(""),
	}
	if req.Principal.Elevation != nil {
		elev["present"] = types.Boolean(true)
		elev["tool"] = types.String(req.Principal.Elevation.Tool)
		elev["argsHash"] = types.String(req.Principal.Elevation.ArgsHash)
		elev["approver"] = types.String(req.Principal.Elevation.Approver)
	}

	return types.NewRecord(types.RecordMap{
		"args":             types.NewRecord(args),
		"argsHash":         types.String(req.ArgsHash),
		"presenceVerified": types.Boolean(req.PresenceVerified),
		"hour":             types.Long(req.Now.In(loc).Hour()),
		"actorChainLength": types.Long(len(req.Principal.ActorChain)),
		"device": types.NewRecord(types.RecordMap{
			"status":                   types.String(dev.Status),
			"enrolled":                 types.Boolean(dev.Enrolled),
			"model":                    types.String(dev.Model),
			"modelAllowed":             types.Boolean(modelAllowed),
			"attestedOSAtLeastMinimum": types.Boolean(attestOK),
			"reportedOSAtLeastMinimum": types.Boolean(reportOK),
			"attestationAgeSeconds":    types.Long(ageSeconds(req.Now, dev.Attested.At)),
			"checkinAgeSeconds":        types.Long(ageSeconds(req.Now, dev.LastCheckin)),
		}),
		"policy": types.NewRecord(types.RecordMap{
			"attestationMaxAgeSeconds": types.Long(int64(req.Policy.AttestationMaxAge / time.Second)),
			"checkinMaxAgeSeconds":     types.Long(int64(req.Policy.CheckinMaxAge / time.Second)),
		}),
		"elevation": types.NewRecord(elev),
	}), nil
}

// ageSeconds is now minus then, clamped so a zero time reads as very old.
func ageSeconds(now, then time.Time) int64 {
	if then.IsZero() {
		return 1 << 40
	}
	d := now.Sub(then)
	if d < 0 {
		return 0
	}
	return int64(d / time.Second)
}

func toCedarValue(v any) (types.Value, error) {
	switch x := v.(type) {
	case string:
		return types.String(x), nil
	case bool:
		return types.Boolean(x), nil
	case float64:
		if x != float64(int64(x)) {
			return nil, fmt.Errorf("non-integer number %v is not allowed in policy context", x)
		}
		return types.Long(int64(x)), nil
	case int:
		return types.Long(int64(x)), nil
	case int64:
		return types.Long(x), nil
	default:
		return nil, fmt.Errorf("unsupported argument type %T", v)
	}
}

// versionAtLeast compares dotted numeric versions. Missing components are zero.
func versionAtLeast(have, min string) (bool, error) {
	h, err := parseVersion(have)
	if err != nil {
		return false, err
	}
	m, err := parseVersion(min)
	if err != nil {
		return false, err
	}
	for i := 0; i < 3; i++ {
		if h[i] != m[i] {
			return h[i] > m[i], nil
		}
	}
	return true, nil
}

func parseVersion(s string) ([3]int, error) {
	var out [3]int
	if s == "" {
		return out, fmt.Errorf("empty version")
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return out, fmt.Errorf("version %q has more than three components", s)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, fmt.Errorf("version %q: bad component %q", s, p)
		}
		out[i] = n
	}
	return out, nil
}
