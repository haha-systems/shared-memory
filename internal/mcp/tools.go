package mcp

// ToolDefinition models MCP tool metadata.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func toolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "memory_write",
			Description: "Store a new short-term or long-term memory entry.",
			InputSchema: jsonSchema(map[string]any{
				"namespace":    propString("Namespace key (e.g. org/repo/branch/task)."),
				"scope":        propStringEnum("Memory scope.", []string{"short", "long"}),
				"content":      propString("Primary memory content."),
				"summary":      propString("Optional summary."),
				"importance":   propNumber("Importance 1-5."),
				"source_agent": propString("Agent identifier."),
				"ttl_seconds":  propNumber("Optional TTL in seconds for short-term memory."),
				"metadata": map[string]any{
					"type": "object",
				},
			}, []string{"namespace", "content"}),
		},
		{
			Name:        "memory_search",
			Description: "Search memory by lexical relevance + recency + importance.",
			InputSchema: jsonSchema(map[string]any{
				"namespace":        propString("Namespace key."),
				"query":            propString("Search query."),
				"scope":            propStringEnum("Optional scope filter.", []string{"short", "long"}),
				"k":                propNumber("Maximum results."),
				"include_metadata": propBoolean("Whether to include metadata in results."),
			}, []string{"namespace", "query"}),
		},
		{
			Name:        "memory_get_context_pack",
			Description: "Return a compact, deduplicated context pack under a token budget.",
			InputSchema: jsonSchema(map[string]any{
				"namespace":    propString("Namespace key."),
				"query":        propString("Query for retrieving context."),
				"token_budget": propNumber("Maximum estimated tokens."),
				"scope":        propStringEnum("Optional scope filter.", []string{"short", "long"}),
				"k":            propNumber("Maximum candidate items to evaluate."),
			}, []string{"namespace", "query", "token_budget"}),
		},
		{
			Name:        "memory_promote",
			Description: "Promote a memory entry to long-term memory.",
			InputSchema: jsonSchema(map[string]any{
				"memory_id":    propString("Memory ID to promote."),
				"target_scope": propStringEnum("Target scope.", []string{"long"}),
				"reason":       propString("Optional reason for promotion."),
			}, []string{"memory_id"}),
		},
	}
}

func jsonSchema(properties map[string]any, required []string) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": properties,
		"required":   required,
	}
}

func propString(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func propStringEnum(description string, values []string) map[string]any {
	return map[string]any{"type": "string", "description": description, "enum": values}
}

func propNumber(description string) map[string]any {
	return map[string]any{"type": "number", "description": description}
}

func propBoolean(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}
