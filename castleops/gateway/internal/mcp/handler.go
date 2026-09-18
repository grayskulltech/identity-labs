// Package mcp is the Streamable HTTP transport for the gateway. One endpoint,
// JSON-RPC 2.0 bodies, initialize handshake, session id, protocol version
// header. Responses are JSON; SSE is not needed for the v1 tool set.
package mcp

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/gateway"
)

// ProtocolVersion is the MCP revision this server speaks.
const ProtocolVersion = "2025-06-18"

// Handler serves MCP over HTTP.
type Handler struct {
	Gateway *gateway.Gateway
	// AllowInsecureDevFingerprint lets a header stand in for the mTLS client
	// certificate. Never set in a real deployment; the serve command logs it.
	AllowInsecureDevFingerprint bool
	ServerName                  string
	ServerVersion               string

	mu       sync.Mutex
	sessions map[string]time.Time
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.post(w, r)
	case http.MethodDelete:
		h.endSession(r.Header.Get("Mcp-Session-Id"))
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) post(w http.ResponseWriter, r *http.Request) {
	if !strings.Contains(r.Header.Get("Accept"), "application/json") {
		http.Error(w, "Accept must include application/json", http.StatusNotAcceptable)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil || req.JSONRPC != "2.0" {
		writeJSON(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", Error: &rpcError{-32700, "parse error"}})
		return
	}

	if req.Method == "initialize" {
		sid := h.newSession()
		w.Header().Set("Mcp-Session-Id", sid)
		writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": h.serverName(), "version": h.ServerVersion},
		}})
		return
	}

	if !h.validSession(r.Header.Get("Mcp-Session-Id")) {
		http.Error(w, "unknown or expired session; initialize first", http.StatusNotFound)
		return
	}
	if v := r.Header.Get("MCP-Protocol-Version"); v != "" && v != ProtocolVersion {
		http.Error(w, "unsupported MCP-Protocol-Version", http.StatusBadRequest)
		return
	}

	switch req.Method {
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "ping":
		writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})
	case "tools/list":
		h.toolsList(w, req)
	case "tools/call":
		h.toolsCall(w, r, req)
	default:
		writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{-32601, "method not found"}})
	}
}

func (h *Handler) toolsList(w http.ResponseWriter, req rpcRequest) {
	var tools []map[string]any
	for _, t := range h.Gateway.Tools.All() {
		tools = append(tools, map[string]any{
			"name":        t.ID,
			"description": t.Description,
			"inputSchema": json.RawMessage(t.Arguments),
			"annotations": map[string]any{
				"readOnlyHint":    t.Kind == "read",
				"destructiveHint": t.Kind != "read",
				"castleops": map[string]any{
					"kind": t.Kind, "domain": t.Domain, "version": t.Version, "worker": t.Worker,
				},
			},
		})
	}
	writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": tools}})
}

func (h *Handler) toolsCall(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil || params.Name == "" {
		writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{-32602, "invalid params"}})
		return
	}
	if params.Arguments == nil {
		params.Arguments = map[string]any{}
	}

	token := ""
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "DPoP ") {
		token = strings.TrimPrefix(auth, "DPoP ")
	}
	fp := peerFingerprint(r)
	if fp == "" && h.AllowInsecureDevFingerprint {
		fp = r.Header.Get("X-CastleOps-Dev-Device-Fingerprint")
	}

	out := h.Gateway.Call(r.Context(), gateway.CallInput{
		RequestID:           newID(),
		PeerCertFingerprint: fp,
		AccessToken:         token,
		Proof:               r.Header.Get("DPoP"),
		Method:              r.Method,
		URL:                 h.Gateway.Endpoint,
		ToolID:              params.Name,
		Args:                params.Arguments,
	})

	result := map[string]any{
		"content": []map[string]any{{"type": "text", "text": out.Content}},
		"isError": out.IsError,
		"_meta": map[string]any{
			"castleops": map[string]any{
				"request_id":           out.Audit.RequestID,
				"decision":             out.Audit.Decision,
				"deny_stage":           out.DenyStage,
				"determining_policies": out.Decision.DeterminingPolicies,
				"guardrail_verdict":    out.Verdict,
			},
		},
	}
	writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
}

func peerFingerprint(r *http.Request) string {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return ""
	}
	sum := sha256.Sum256(r.TLS.PeerCertificates[0].Raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (h *Handler) serverName() string {
	if h.ServerName == "" {
		return "castleops-gateway"
	}
	return h.ServerName
}

func (h *Handler) newSession() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions == nil {
		h.sessions = map[string]time.Time{}
	}
	id := newID()
	h.sessions[id] = time.Now().Add(12 * time.Hour)
	return id
}

func (h *Handler) validSession(id string) bool {
	if id == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	exp, ok := h.sessions[id]
	if !ok || time.Now().After(exp) {
		delete(h.sessions, id)
		return false
	}
	return true
}

func (h *Handler) endSession(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.sessions, id)
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
