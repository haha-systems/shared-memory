package types

import "time"

// MemoryRecord represents one persisted memory item.
type MemoryRecord struct {
	ID             string         `json:"id"`
	Namespace      string         `json:"namespace"`
	Scope          string         `json:"scope"`
	Content        string         `json:"content"`
	Summary        string         `json:"summary"`
	Importance     int            `json:"importance"`
	SourceAgent    string         `json:"source_agent,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	LastAccessedAt time.Time      `json:"last_accessed_at"`
	ExpiresAt      *time.Time     `json:"expires_at,omitempty"`
	PromotedAt     *time.Time     `json:"promoted_at,omitempty"`
}

// WriteInput describes a new memory write operation.
type WriteInput struct {
	Namespace   string         `json:"namespace"`
	Scope       string         `json:"scope"`
	Content     string         `json:"content"`
	Summary     string         `json:"summary,omitempty"`
	Importance  int            `json:"importance,omitempty"`
	SourceAgent string         `json:"source_agent,omitempty"`
	TTLSeconds  int            `json:"ttl_seconds,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// SearchInput is used for search operations.
type SearchInput struct {
	Namespace       string `json:"namespace"`
	Query           string `json:"query"`
	Scope           string `json:"scope,omitempty"`
	K               int    `json:"k,omitempty"`
	IncludeMetadata bool   `json:"include_metadata,omitempty"`
}

// SearchResult is a ranked item from search.
type SearchResult struct {
	Record          MemoryRecord `json:"record"`
	Score           float64      `json:"score"`
	LexicalScore    float64      `json:"lexical_score"`
	RecencyScore    float64      `json:"recency_score"`
	ImportanceScore float64      `json:"importance_score"`
}

// ContextPackInput requests a compact context bundle.
type ContextPackInput struct {
	Namespace   string `json:"namespace"`
	Query       string `json:"query"`
	TokenBudget int    `json:"token_budget"`
	Scope       string `json:"scope,omitempty"`
	K           int    `json:"k,omitempty"`
}

// ContextPack is optimized for prompt injection into agents.
type ContextPack struct {
	Text            string   `json:"text"`
	EstimatedTokens int      `json:"estimated_tokens"`
	MemoryIDs       []string `json:"memory_ids"`
}

// PromoteInput promotes an item to long-term memory.
type PromoteInput struct {
	MemoryID    string `json:"memory_id"`
	TargetScope string `json:"target_scope"`
	Reason      string `json:"reason,omitempty"`
}
