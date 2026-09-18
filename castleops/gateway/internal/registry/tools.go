// Package registry holds the tool registry and the device registry. Both are
// loaded or mutated only through typed APIs; nothing edits them in place.
package registry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/grayskulltech/identity-labs/castleops/gateway/internal/contract"
)

// ToolRegistry is the set of tools the gateway will ever execute.
type ToolRegistry struct {
	tools    map[string]contract.Tool
	schemas  map[string]*jsonschema.Schema
	defaults map[string]map[string]any
}

// LoadTools reads every *.json under dir as a tool definition and compiles
// its argument schema. A malformed tool is a startup failure, not a warning.
func LoadTools(dir string) (*ToolRegistry, error) {
	entries, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no tool definitions in %s", dir)
	}
	reg := &ToolRegistry{
		tools:    map[string]contract.Tool{},
		schemas:  map[string]*jsonschema.Schema{},
		defaults: map[string]map[string]any{},
	}
	for _, path := range entries {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if err := reg.add(raw, filepath.Base(path)); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	return reg, nil
}

// LoadToolsFromBytes builds a registry from in-memory definitions.
func LoadToolsFromBytes(defs map[string][]byte) (*ToolRegistry, error) {
	reg := &ToolRegistry{
		tools:    map[string]contract.Tool{},
		schemas:  map[string]*jsonschema.Schema{},
		defaults: map[string]map[string]any{},
	}
	names := make([]string, 0, len(defs))
	for n := range defs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := reg.add(defs[n], n); err != nil {
			return nil, fmt.Errorf("%s: %w", n, err)
		}
	}
	return reg, nil
}

func (r *ToolRegistry) add(raw []byte, name string) error {
	var t contract.Tool
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	if err := validateToolShape(t); err != nil {
		return err
	}
	if _, dup := r.tools[t.ID]; dup {
		return fmt.Errorf("duplicate tool id %q", t.ID)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(t.Arguments))
	if err != nil {
		return fmt.Errorf("arguments schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	url := "mem://castleops/tools/" + t.ID + ".json"
	if err := c.AddResource(url, doc); err != nil {
		return fmt.Errorf("arguments schema: %w", err)
	}
	sch, err := c.Compile(url)
	if err != nil {
		return fmt.Errorf("arguments schema: %w", err)
	}
	r.tools[t.ID] = t
	r.schemas[t.ID] = sch
	r.defaults[t.ID] = extractDefaults(doc)
	return nil
}

func validateToolShape(t contract.Tool) error {
	switch {
	case t.ID == "" || !strings.Contains(t.ID, "."):
		return fmt.Errorf("tool id %q must be dotted", t.ID)
	case t.Version < 1:
		return fmt.Errorf("tool %s: version must be >= 1", t.ID)
	case t.Kind != contract.ToolRead && t.Kind != contract.ToolWrite && t.Kind != contract.ToolElevate:
		return fmt.Errorf("tool %s: kind %q", t.ID, t.Kind)
	case t.Domain == "":
		return fmt.Errorf("tool %s: domain required", t.ID)
	case !strings.HasPrefix(t.Worker, "castleops-"):
		return fmt.Errorf("tool %s: worker must be a castleops-* NHI", t.ID)
	case t.Result.MaxBytes < 1 || t.Result.MaxBytes > 8192:
		return fmt.Errorf("tool %s: result.max_bytes out of range", t.ID)
	}
	var argShape struct {
		Type                 string         `json:"type"`
		AdditionalProperties *bool          `json:"additionalProperties"`
		Properties           map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(t.Arguments, &argShape); err != nil {
		return fmt.Errorf("tool %s: arguments: %w", t.ID, err)
	}
	if argShape.Type != "object" || argShape.AdditionalProperties == nil || *argShape.AdditionalProperties {
		return fmt.Errorf("tool %s: arguments must be an object with additionalProperties false", t.ID)
	}
	for hint := range t.PolicyHints {
		if _, ok := argShape.Properties[hint]; !ok {
			return fmt.Errorf("tool %s: policy_hints.%s is not an argument", t.ID, hint)
		}
	}
	return nil
}

func extractDefaults(doc any) map[string]any {
	out := map[string]any{}
	m, ok := doc.(map[string]any)
	if !ok {
		return out
	}
	props, ok := m["properties"].(map[string]any)
	if !ok {
		return out
	}
	for name, p := range props {
		if pm, ok := p.(map[string]any); ok {
			if d, has := pm["default"]; has {
				out[name] = d
			}
		}
	}
	return out
}

// Get returns a tool by id.
func (r *ToolRegistry) Get(id string) (contract.Tool, bool) {
	t, ok := r.tools[id]
	return t, ok
}

// All returns tools sorted by id.
func (r *ToolRegistry) All() []contract.Tool {
	out := make([]contract.Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Normalize applies schema defaults and validates args against the tool's
// argument schema. It returns the normalized arguments, which are the only
// arguments the rest of the pipeline ever sees.
func (r *ToolRegistry) Normalize(id string, args map[string]any) (map[string]any, error) {
	sch, ok := r.schemas[id]
	if !ok {
		return nil, fmt.Errorf("unknown tool %q", id)
	}
	merged := map[string]any{}
	for k, v := range r.defaults[id] {
		merged[k] = v
	}
	for k, v := range args {
		merged[k] = v
	}
	// Round-trip through JSON so numbers are json.Number, which the validator
	// treats as exact integers where the schema says integer.
	buf, err := json.Marshal(merged)
	if err != nil {
		return nil, err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	if err := sch.Validate(doc); err != nil {
		return nil, fmt.Errorf("arguments rejected: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(buf, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ArgsHash is sha256 over the canonical JSON of the normalized arguments.
// encoding/json emits object keys sorted, which is the canonical form we rely on.
func ArgsHash(args map[string]any) string {
	buf, _ := json.Marshal(args)
	sum := sha256.Sum256(buf)
	return "sha256:" + hex.EncodeToString(sum[:])
}
