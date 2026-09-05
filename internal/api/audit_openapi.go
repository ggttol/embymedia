package api

func auditLogParameters() []any {
	return []any{
		map[string]any{"name": "limit", "in": "query", "description": "Page size", "schema": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 20}},
		map[string]any{"name": "offset", "in": "query", "description": "Records to skip", "schema": map[string]any{"type": "integer", "minimum": 0, "default": 0}},
		map[string]any{"name": "protocol", "in": "query", "description": "Ingress protocol", "schema": map[string]any{"type": "string", "enum": []string{"all", "mcp"}, "default": "all"}},
		map[string]any{"name": "agent", "in": "query", "description": "Exact agent name", "schema": map[string]any{"type": "string"}},
		map[string]any{"name": "action", "in": "query", "description": "Exact tool or action", "schema": map[string]any{"type": "string"}},
		map[string]any{"name": "status", "in": "query", "description": "Execution status", "schema": map[string]any{"type": "string", "enum": []string{"success", "error", "denied"}}},
		map[string]any{"name": "from", "in": "query", "description": "Inclusive lower timestamp bound", "schema": map[string]any{"type": "string", "format": "date-time"}},
		map[string]any{"name": "to", "in": "query", "description": "Inclusive upper timestamp bound", "schema": map[string]any{"type": "string", "format": "date-time"}},
		map[string]any{"name": "q", "in": "query", "description": "Literal substring in action, agent name, input, or output", "schema": map[string]any{"type": "string"}},
	}
}
