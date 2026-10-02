package tools

// Small helpers that build JSON Schema fragments, so tool definitions read
// as a list of arguments instead of nested maps.

type props map[string]any

func object(p props, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": map[string]any(p), "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func integer(desc string, min, max, def int) map[string]any {
	s := map[string]any{"type": "integer", "description": desc}
	if min != 0 || max != 0 {
		s["minimum"], s["maximum"] = min, max
	}
	if def != 0 {
		s["default"] = def
	}
	return s
}

func text(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func enum(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": values}
}

func boolean(desc string, def bool) map[string]any {
	return map[string]any{"type": "boolean", "description": desc, "default": def}
}

var ticketID = map[string]any{"type": "integer", "minimum": 1, "description": "The Freshdesk ticket number."}
