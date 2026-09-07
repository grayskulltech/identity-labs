// Package guardrail holds GuardrailScanner adapters. PatternScanner is the
// placeholder for the local classifier described in ADR-002. It exists so the
// pipeline stage, the verdict handling, and the tests are real now; the model
// behind it is swapped in without touching the gateway.
package guardrail

import (
	"context"
	"strings"

	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/contract"
)

// PatternScanner blocks tool results that carry obvious instruction-injection
// markers. It is deliberately conservative: a false block costs a retry, a
// false pass costs a poisoned model context.
type PatternScanner struct{}

func (PatternScanner) Name() string        { return "pattern-scanner (placeholder for local classifier)" }
func (PatternScanner) Tier() contract.Tier { return contract.TierLAN }

var blockedPhrases = []string{
	"ignore previous instructions",
	"ignore all previous",
	"ignore the above",
	"disregard your instructions",
	"you are now",
	"new instructions:",
	"system prompt",
	"<tool_result>",
	"</tool_result>",
	"call the tool",
	"invoke the tool",
	"request elevation",
	"approve this",
}

var suspiciousPhrases = []string{
	"assistant:",
	"user:",
	"###",
	"```",
}

// ScanResult implements contract.GuardrailScanner.
func (PatternScanner) ScanResult(_ context.Context, _ string, content string) (contract.ScanVerdict, error) {
	lower := strings.ToLower(content)
	var findings []string
	for _, p := range blockedPhrases {
		if strings.Contains(lower, p) {
			findings = append(findings, "blocked phrase: "+p)
		}
	}
	if len(findings) > 0 {
		return contract.ScanVerdict{Verdict: "blocked", Findings: findings}, nil
	}
	for _, p := range suspiciousPhrases {
		if strings.Contains(lower, p) {
			findings = append(findings, "suspicious marker: "+p)
		}
	}
	if len(findings) > 0 {
		return contract.ScanVerdict{Verdict: "suspicious", Findings: findings}, nil
	}
	return contract.ScanVerdict{Verdict: "clean"}, nil
}

// BlockedNotice is the fixed text returned in place of blocked content.
const BlockedNotice = "This result was withheld by the household guardrail. Ask again or check the audit log."
