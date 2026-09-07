package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/authn"
	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/sim"
)

// TestStreamableHTTPRoundTrip drives the transport the way the phone does:
// initialize, tools/list, then a tools/call with a DPoP-bound token and proof.
func TestStreamableHTTPRoundTrip(t *testing.T) {
	wd, _ := os.Getwd()
	dir, err := sim.DefaultToolsDir(wd)
	if err != nil {
		t.Fatal(err)
	}
	w, err := sim.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Gateway: w.Gateway, AllowInsecureDevFingerprint: true, ServerVersion: "test"}
	srv := httptest.NewServer(h)
	defer srv.Close()

	post := func(body string, headers map[string]string) (*http.Response, map[string]any) {
		req, _ := http.NewRequest(http.MethodPost, srv.URL, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if resp.StatusCode == http.StatusOK {
			_ = json.NewDecoder(resp.Body).Decode(&out)
		}
		resp.Body.Close()
		return resp, out
	}

	// A call before initialize is refused.
	resp, _ := post(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("tools/list without session: got %d", resp.StatusCode)
	}

	resp, out := post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`, nil)
	sid := resp.Header.Get("Mcp-Session-Id")
	if resp.StatusCode != http.StatusOK || sid == "" {
		t.Fatalf("initialize: status %d session %q", resp.StatusCode, sid)
	}
	if pv := out["result"].(map[string]any)["protocolVersion"]; pv != ProtocolVersion {
		t.Fatalf("protocolVersion %v", pv)
	}
	std := map[string]string{"Mcp-Session-Id": sid, "MCP-Protocol-Version": ProtocolVersion}

	resp, _ = post(`{"jsonrpc":"2.0","method":"notifications/initialized"}`, std)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("initialized notification: %d", resp.StatusCode)
	}

	_, out = post(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, std)
	tools := out["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools))
	}

	// Mint what the IdP and the phone would produce.
	gary := w.People["gary"]
	now := w.Clock.T
	token, err := authn.MintAccessToken(w.IdPKey, sim.IdPKid, sim.Issuer, sim.Audience, gary.Subject, gary.Groups, now, 10*time.Minute, map[string]any{
		"cnf": map[string]any{"jkt": authn.JWKFromPublic(&gary.Phone.SessionKey.PublicKey, "").Thumbprint()},
	})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := authn.MintDPoPProof(gary.Phone.SessionKey, "POST", sim.Endpoint, token, now)
	if err != nil {
		t.Fatal(err)
	}
	callHeaders := map[string]string{
		"Mcp-Session-Id": sid, "MCP-Protocol-Version": ProtocolVersion,
		"Authorization": "DPoP " + token, "DPoP": proof,
		"X-CastleOps-Dev-Device-Fingerprint": gary.Phone.Fingerprint,
	}
	_, out = post(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"calendar.events.read","arguments":{"window":"today"}}}`, callHeaders)
	result := out["result"].(map[string]any)
	if result["isError"] != false {
		t.Fatalf("expected success, got %v", result)
	}
	meta := result["_meta"].(map[string]any)["castleops"].(map[string]any)
	if meta["decision"] != "allow" || meta["guardrail_verdict"] != "clean" {
		t.Fatalf("meta %v", meta)
	}

	// The same proof again is a replay and must be denied at attestation.
	_, out = post(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"calendar.events.read","arguments":{"window":"today"}}}`, callHeaders)
	result = out["result"].(map[string]any)
	meta = result["_meta"].(map[string]any)["castleops"].(map[string]any)
	if result["isError"] != true || meta["deny_stage"] != "attestation" {
		t.Fatalf("replay should deny at attestation, got %v", result)
	}

	// Without the device fingerprint nothing is even considered.
	delete(callHeaders, "X-CastleOps-Dev-Device-Fingerprint")
	_, out = post(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"calendar.events.read","arguments":{"window":"today"}}}`, callHeaders)
	meta = out["result"].(map[string]any)["_meta"].(map[string]any)["castleops"].(map[string]any)
	if meta["deny_stage"] != "transport" {
		t.Fatalf("missing device cert should deny at transport, got %v", meta)
	}

	// End the session; a later call is refused.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL, nil)
	req.Header.Set("Mcp-Session-Id", sid)
	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete session: %v %v", err, resp)
	}
	resp, _ = post(`{"jsonrpc":"2.0","id":6,"method":"tools/list"}`, std)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("tools/list after delete: got %d", resp.StatusCode)
	}
}
