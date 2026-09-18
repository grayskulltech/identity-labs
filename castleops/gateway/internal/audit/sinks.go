// Package audit holds AuditSink adapters.
package audit

import (
	"context"
	"encoding/json"
	"os"
	"sync"

	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/contract"
)

// MemorySink keeps records in memory. Tests and the simulator read them back.
type MemorySink struct {
	mu   sync.Mutex
	recs []contract.AuditRecord
}

func (m *MemorySink) Name() string        { return "memory-audit" }
func (m *MemorySink) Tier() contract.Tier { return contract.TierLAN }

// Write implements contract.AuditSink.
func (m *MemorySink) Write(_ context.Context, rec contract.AuditRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recs = append(m.recs, rec)
	return nil
}

// Records returns a copy of everything written.
func (m *MemorySink) Records() []contract.AuditRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]contract.AuditRecord, len(m.recs))
	copy(out, m.recs)
	return out
}

// JSONLSink appends one JSON object per line to a file. Loki or Splunk tails it.
type JSONLSink struct {
	mu sync.Mutex
	f  *os.File
}

// OpenJSONL opens or creates the file in append-only mode.
func OpenJSONL(path string) (*JSONLSink, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &JSONLSink{f: f}, nil
}

func (j *JSONLSink) Name() string        { return "jsonl-audit" }
func (j *JSONLSink) Tier() contract.Tier { return contract.TierLAN }

// Write implements contract.AuditSink.
func (j *JSONLSink) Write(_ context.Context, rec contract.AuditRecord) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	_, err = j.f.Write(append(b, '\n'))
	return err
}

// Close closes the file.
func (j *JSONLSink) Close() error { return j.f.Close() }
