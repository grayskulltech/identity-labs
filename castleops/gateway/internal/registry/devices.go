package registry

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/contract"
)

// MemoryDevices is the in-memory device registry. It implements
// contract.DevicePosture and is the revocation point described in ADR-003.
// The production store persists to disk on the lab host and is backed up with
// the IdP; the API is the same.
type MemoryDevices struct {
	mu            sync.RWMutex
	byID          map[string]contract.PostureRecord
	byFingerprint map[string]string // fingerprint -> device id
}

// NewMemoryDevices returns an empty registry.
func NewMemoryDevices() *MemoryDevices {
	return &MemoryDevices{byID: map[string]contract.PostureRecord{}, byFingerprint: map[string]string{}}
}

func (m *MemoryDevices) Name() string        { return "memory-device-registry" }
func (m *MemoryDevices) Tier() contract.Tier { return contract.TierLAN }

// Upsert writes a record. Enrollment and posture updates both land here.
func (m *MemoryDevices) Upsert(rec contract.PostureRecord) error {
	if rec.DeviceID == "" || rec.Subject == "" {
		return fmt.Errorf("device record needs device_id and subject")
	}
	if !strings.HasPrefix(rec.Keys.DeviceCertFingerprint, "sha256:") {
		return fmt.Errorf("device %s: device_cert_fingerprint must be sha256:", rec.DeviceID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.byID[rec.DeviceID]; ok {
		delete(m.byFingerprint, old.Keys.DeviceCertFingerprint)
	}
	m.byID[rec.DeviceID] = rec
	m.byFingerprint[rec.Keys.DeviceCertFingerprint] = rec.DeviceID
	return nil
}

// Lookup accepts a UDID or a sha256: certificate fingerprint.
func (m *MemoryDevices) Lookup(_ context.Context, deviceID string) (contract.PostureRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id := deviceID
	if strings.HasPrefix(deviceID, "sha256:") {
		mapped, ok := m.byFingerprint[deviceID]
		if !ok {
			return contract.PostureRecord{}, contract.Deny(contract.StageDevice, "unknown device certificate")
		}
		id = mapped
	}
	rec, ok := m.byID[id]
	if !ok {
		return contract.PostureRecord{}, contract.Deny(contract.StageDevice, "unknown device")
	}
	return rec, nil
}

// Revoke flips a device to revoked. Every later request from it is denied at
// the device stage with no token revocation required.
func (m *MemoryDevices) Revoke(_ context.Context, deviceID, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.byID[deviceID]
	if !ok {
		return fmt.Errorf("unknown device %s", deviceID)
	}
	rec.Status = "revoked"
	m.byID[deviceID] = rec
	_ = reason // recorded by the audit sink at the call site
	return nil
}

// AdvanceCounter enforces the App Attest monotonic counter. A counter that
// does not advance is a replay and is refused.
func (m *MemoryDevices) AdvanceCounter(deviceID string, next uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.byID[deviceID]
	if !ok {
		return fmt.Errorf("unknown device %s", deviceID)
	}
	if next <= rec.Keys.AttestCounter {
		return contract.Deny(contract.StageAttestation, "assertion counter did not advance")
	}
	rec.Keys.AttestCounter = next
	m.byID[deviceID] = rec
	return nil
}
