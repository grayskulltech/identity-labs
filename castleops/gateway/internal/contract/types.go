// Package contract holds the Go types and adapter interfaces that mirror
// castleops/contract/v1. The JSON schemas in that directory are the source
// of truth; these types exist so the reference gateway can compile against
// them. Field names and JSON tags follow the schemas exactly.
package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Tier is the data tier an adapter's outbound traffic occupies (ADR-001).
type Tier int

const (
	TierLAN    Tier = 1 // stays on the Kingsbrook LAN
	TierOpaque Tier = 2 // opaque references may transit a third party
	TierLeaks  Tier = 3 // argument values or content leave the LAN; Cisco track only
)

// Adapter is implemented by every pluggable component so the gateway can
// refuse to load one whose tier exceeds the deployment's allowance.
type Adapter interface {
	Name() string
	Tier() Tier
}

// ToolKind decides which gates apply before execution.
type ToolKind string

const (
	ToolRead    ToolKind = "read"
	ToolWrite   ToolKind = "write"
	ToolElevate ToolKind = "elevate"
)

// Tool is one registry entry (tool.schema.json).
type Tool struct {
	ID          string            `json:"id"`
	Version     int               `json:"version"`
	Kind        ToolKind          `json:"kind"`
	Domain      string            `json:"domain"`
	Description string            `json:"description"`
	Arguments   json.RawMessage   `json:"arguments"`
	Result      ToolResult        `json:"result"`
	Worker      string            `json:"worker"`
	PolicyHints map[string]string `json:"policy_hints,omitempty"`
}

// ToolResult bounds what a tool may return (results are compact by contract).
type ToolResult struct {
	MaxBytes int    `json:"max_bytes"`
	Shape    string `json:"shape"`
}

// ToolRef is the subset of Tool that policy sees.
type ToolRef struct {
	ID      string   `json:"id"`
	Version int      `json:"version"`
	Kind    ToolKind `json:"kind"`
	Domain  string   `json:"domain"`
}

// PrincipalAssertion is what IdentityProvider produces from a token
// (principal-assertion.schema.json).
type PrincipalAssertion struct {
	Subject      string        `json:"subject"`
	Groups       []string      `json:"groups"`
	IssuedAt     time.Time     `json:"issued_at"`
	ExpiresAt    time.Time     `json:"expires_at"`
	AuthTime     time.Time     `json:"auth_time"`
	ActorChain   []string      `json:"actor_chain"`
	Confirmation *Confirmation `json:"confirmation,omitempty"`
	Elevation    *Elevation    `json:"elevation,omitempty"`
}

// Confirmation is the key binding (cnf) on a sender-constrained token.
type Confirmation struct {
	JKT string `json:"jkt"`
}

// Elevation is present only on a single-use break-glass token.
type Elevation struct {
	Tool          string `json:"tool"`
	ArgsHash      string `json:"args_hash"`
	TransactionID string `json:"transaction_id"`
	Approver      string `json:"approver"`
}

// PostureRecord is what DevicePosture knows about a device
// (posture-record.schema.json).
type PostureRecord struct {
	DeviceID    string     `json:"device_id"`
	Subject     string     `json:"subject"`
	Status      string     `json:"status"` // pending | active | revoked
	Model       string     `json:"model"`
	Enrolled    bool       `json:"enrolled"`
	Supervised  bool       `json:"supervised"`
	LastCheckin time.Time  `json:"last_checkin"`
	Attested    Attested   `json:"attested"`
	Reported    Reported   `json:"reported"`
	Keys        DeviceKeys `json:"keys"`
	ApprovedBy  string     `json:"approved_by,omitempty"`
	ApprovedAt  *time.Time `json:"approved_at,omitempty"`
}

// Attested comes from Managed Device Attestation. Apple vouches for these.
type Attested struct {
	OSVersion   string    `json:"os_version"`
	At          time.Time `json:"at"`
	Serial      string    `json:"serial"`
	SEPFirmware string    `json:"sep_firmware,omitempty"`
}

// Reported comes from Declarative Device Management status. Timely, not attested.
type Reported struct {
	OSVersion string    `json:"os_version"`
	At        time.Time `json:"at"`
}

// DeviceKeys are the public halves the gateway binds requests to.
type DeviceKeys struct {
	DeviceCertFingerprint string `json:"device_cert_fingerprint"`
	AttestKeyID           string `json:"attest_key_id"`
	AttestCounter         uint64 `json:"attest_counter"`
	PresenceKeyJKT        string `json:"presence_key_jkt"`
}

// PolicyParams are deployment-wide knobs the policy reads as context.
type PolicyParams struct {
	MinimumOS         string        `json:"minimum_os"`
	AttestationMaxAge time.Duration `json:"attestation_max_age"`
	CheckinMaxAge     time.Duration `json:"checkin_max_age"`
	AllowedModels     []string      `json:"allowed_models"`
	HouseholdTimeZone string        `json:"household_time_zone"`
}

// AuthzRequest is handed to PolicyDecisionPoint (authz-request.schema.json).
type AuthzRequest struct {
	RequestID        string             `json:"request_id"`
	Now              time.Time          `json:"now"`
	Principal        PrincipalAssertion `json:"principal"`
	Tool             ToolRef            `json:"tool"`
	Args             map[string]any     `json:"args"`
	ArgsHash         string             `json:"args_hash"`
	Device           PostureRecord      `json:"device"`
	PresenceVerified bool               `json:"presence_verified"`
	Policy           PolicyParams       `json:"policy"`
}

// AuthzDecision is returned by PolicyDecisionPoint (authz-decision.schema.json).
type AuthzDecision struct {
	RequestID           string   `json:"request_id"`
	Decision            string   `json:"decision"` // allow | deny
	DeterminingPolicies []string `json:"determining_policies"`
	Errors              []string `json:"errors"`
	Reason              string   `json:"reason,omitempty"`
}

// Allowed reports whether the decision permits execution.
func (d AuthzDecision) Allowed() bool { return d.Decision == "allow" && len(d.Errors) == 0 }

// AuditRecord is written by AuditSink (audit-record.schema.json).
type AuditRecord struct {
	RequestID              string    `json:"request_id"`
	At                     time.Time `json:"at"`
	Subject                string    `json:"subject"`
	DeviceID               string    `json:"device_id"`
	ActorChain             []string  `json:"actor_chain"`
	Tool                   string    `json:"tool"`
	ToolVersion            int       `json:"tool_version,omitempty"`
	ArgsHash               string    `json:"args_hash"`
	PresenceVerified       bool      `json:"presence_verified"`
	Decision               string    `json:"decision"`
	DeterminingPolicies    []string  `json:"determining_policies"`
	DenyStage              string    `json:"deny_stage,omitempty"`
	Outcome                string    `json:"outcome"` // executed | denied | failed | held_for_approval
	ElevationTransactionID string    `json:"elevation_transaction_id,omitempty"`
	GuardrailVerdict       string    `json:"guardrail_verdict,omitempty"`
}

// ApprovalRequest is handed to ApprovalChannel (approval-request.schema.json).
type ApprovalRequest struct {
	TransactionID   string         `json:"transaction_id"`
	Kind            string         `json:"kind"` // break_glass | device_enrollment
	Requester       string         `json:"requester"`
	RequesterDevice string         `json:"requester_device"`
	CreatedAt       time.Time      `json:"created_at"`
	ExpiresAt       time.Time      `json:"expires_at"`
	Approvers       []string       `json:"approvers"`
	RequestHash     string         `json:"request_hash"`
	Tool            string         `json:"tool,omitempty"`
	ArgsHash        string         `json:"args_hash,omitempty"`
	DeviceSummary   map[string]any `json:"device_summary,omitempty"`
}

// ApprovalDecision is the approver's signed answer.
type ApprovalDecision struct {
	Approved  bool               `json:"approved"`
	Approver  PrincipalAssertion `json:"approver"`
	Signature string             `json:"signature"` // by the approver's presence key over RequestHash
}

// ScanVerdict is the guardrail outcome for one tool result.
type ScanVerdict struct {
	Verdict  string   `json:"verdict"` // clean | suspicious | blocked
	Findings []string `json:"findings,omitempty"`
}

// Stage names a pipeline stage for deny attribution. Values match
// audit-record.schema.json deny_stage.
type Stage string

const (
	StageTransport   Stage = "transport"
	StageToken       Stage = "token"
	StageDevice      Stage = "device"
	StageAttestation Stage = "attestation"
	StagePosture     Stage = "posture"
	StageSchema      Stage = "schema"
	StagePolicy      Stage = "policy"
	StagePresence    Stage = "presence"
	StageExecution   Stage = "execution"
	StageGuardrail   Stage = "guardrail"
)

// Error kinds, per interfaces.md. Unavailable on a decision-path adapter is
// treated as deny by the gateway.
var (
	ErrDeny        = errors.New("deny")
	ErrUnavailable = errors.New("unavailable")
	ErrInvalid     = errors.New("invalid")
)

// StageError carries the stage that produced a typed error.
type StageError struct {
	Stage Stage
	Kind  error // ErrDeny, ErrUnavailable, ErrInvalid
	Msg   string
}

func (e *StageError) Error() string { return fmt.Sprintf("%s: %v: %s", e.Stage, e.Kind, e.Msg) }
func (e *StageError) Unwrap() error { return e.Kind }

// Deny builds a deny error for a stage.
func Deny(stage Stage, format string, a ...any) error {
	return &StageError{Stage: stage, Kind: ErrDeny, Msg: fmt.Sprintf(format, a...)}
}

// Unavailable builds an unavailable error for a stage.
func Unavailable(stage Stage, format string, a ...any) error {
	return &StageError{Stage: stage, Kind: ErrUnavailable, Msg: fmt.Sprintf(format, a...)}
}

// Invalid builds an invalid-input error for a stage.
func Invalid(stage Stage, format string, a ...any) error {
	return &StageError{Stage: stage, Kind: ErrInvalid, Msg: fmt.Sprintf(format, a...)}
}

// IdentityProvider authenticates people and exchanges tokens for agents.
type IdentityProvider interface {
	Adapter
	VerifyAccessToken(ctx context.Context, token string) (PrincipalAssertion, error)
}

// PolicyDecisionPoint decides one tool call. Fail closed.
type PolicyDecisionPoint interface {
	Adapter
	Decide(ctx context.Context, req AuthzRequest) (AuthzDecision, error)
	Validate() error
}

// DevicePosture answers what the gateway may rely on about a device now.
type DevicePosture interface {
	Adapter
	// Lookup accepts an MDM UDID or a "sha256:..." device certificate fingerprint.
	Lookup(ctx context.Context, deviceID string) (PostureRecord, error)
	Revoke(ctx context.Context, deviceID, reason string) error
}

// GuardrailScanner scans tool results before they return to the phone.
type GuardrailScanner interface {
	Adapter
	ScanResult(ctx context.Context, toolID, content string) (ScanVerdict, error)
}

// ApprovalChannel delivers an approval request and returns the signed decision.
type ApprovalChannel interface {
	Adapter
	RequestApproval(ctx context.Context, req ApprovalRequest) (string, error)
	AwaitDecision(ctx context.Context, transactionID string, timeout time.Duration) (ApprovalDecision, error)
}

// AuditSink is append-only and must not block the decision path.
type AuditSink interface {
	Adapter
	Write(ctx context.Context, rec AuditRecord) error
}

// BindingInput is what a DeviceBinding needs to verify that this request
// came from the enrolled device, now.
type BindingInput struct {
	Method      string
	URL         string
	AccessToken string
	Proof       string // DPoP proof JWT or App Attest assertion, per implementation
	Device      PostureRecord
	Now         time.Time
}

// BindingResult says which enrolled key signed the request.
type BindingResult struct {
	KeyThumbprint string
	Presence      bool // signed by the biometry-gated presence key
}

// DeviceBinding is the per-request sender constraint. Two implementations are
// planned: DPoP (dev and non-Apple clients) and App Attest (ADR-003).
type DeviceBinding interface {
	Adapter
	Verify(ctx context.Context, in BindingInput) (BindingResult, error)
}
