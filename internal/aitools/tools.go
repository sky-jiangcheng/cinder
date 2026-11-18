// Package aitools owns the tool-calling contract: the registry of callable
// tools, the OpenAI-compatible tool-call wires, and the model-tool-model loop.
// Tool IMPLEMENTATIONS stay with their domain - internal/service registers
// handlers that read the DB, git and search; this package only knows how to ask
// the model, recognise the calls it emits, and feed results back.
package aitools

import (
	"context"
	"encoding/json"
	"fmt"

	"repotory/internal/aiclient"
)

// ToolFunc is the server-side implementation of one callable tool.
type ToolFunc func(ctx context.Context, args map[string]any) (any, error)

// Registry maps a tool name to its OpenAI definition and implementation.
type Registry struct {
	defs  []aiclient.ToolDefinition
	funcs map[string]ToolFunc
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{funcs: make(map[string]ToolFunc)}
}

// Register adds a tool. A duplicate name overwrites the handler but appends a
// second definition, matching the old behaviour; nothing registers twice today.
func (r *Registry) Register(name, desc string, parameters any, fn ToolFunc) {
	def := aiclient.ToolDefinition{Type: "function"}
	def.Function.Name = name
	def.Function.Description = desc
	def.Function.Parameters = parameters
	r.defs = append(r.defs, def)
	r.funcs[name] = fn
}

// Definitions returns the tools array for a request body; nil when empty so the
// field is omitted rather than sent as [].
func (r *Registry) Definitions() []aiclient.ToolDefinition {
	if len(r.defs) == 0 {
		return nil
	}
	return r.defs
}

// Call decodes the model's argument JSON and runs one tool. Unknown names and
// malformed arguments are errors rather than silent no-ops: the model gets the
// error text back as the tool result and can correct itself next round.
func (r *Registry) Call(ctx context.Context, name, rawArgs string) (any, error) {
	fn, ok := r.funcs[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	var args map[string]any
	if rawArgs != "" {
		if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
			return nil, fmt.Errorf("invalid arguments for %s: %w", name, err)
		}
	}
	return fn(ctx, args)
}
