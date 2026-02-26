package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/google/uuid"

	"github.com/xiy/memory-mcp/internal/config"
	"github.com/xiy/memory-mcp/internal/store"
	"github.com/xiy/memory-mcp/pkg/types"
)

// Service coordinates validation, ranking and retrieval behavior.
type Service struct {
	store         store.Store
	cfg           config.Config
	namespaceExpr *regexp.Regexp
	logger        *log.Logger
}

// NewService constructs a memory service.
func NewService(st store.Store, cfg config.Config, logger *log.Logger) (*Service, error) {
	re, err := regexp.Compile(cfg.NamespacePattern)
	if err != nil {
		return nil, fmt.Errorf("compile namespace pattern: %w", err)
	}
	return &Service{store: st, cfg: cfg, namespaceExpr: re, logger: logger}, nil
}

// Write validates and stores a memory record.
func (s *Service) Write(ctx context.Context, in types.WriteInput) (types.MemoryRecord, error) {
	if err := s.validateNamespace(in.Namespace); err != nil {
		return types.MemoryRecord{}, err
	}
	in.Scope = strings.TrimSpace(strings.ToLower(in.Scope))
	if in.Scope == "" {
		in.Scope = "short"
	}
	if in.Scope != "short" && in.Scope != "long" {
		return types.MemoryRecord{}, fmt.Errorf("invalid scope %q", in.Scope)
	}
	if strings.TrimSpace(in.Content) == "" {
		return types.MemoryRecord{}, errors.New("content must not be empty")
	}

	now := time.Now().UTC()
	importance := in.Importance
	if importance < 1 || importance > 5 {
		importance = 3
	}

	summary := strings.TrimSpace(in.Summary)
	if summary == "" {
		summary = autoSummary(in.Content)
	}

	var expiresAt *time.Time
	if in.Scope == "short" {
		ttlSeconds := in.TTLSeconds
		if ttlSeconds <= 0 {
			ttlSeconds = s.cfg.DefaultShortTTLHours * 3600
		}
		t := now.Add(time.Duration(ttlSeconds) * time.Second)
		expiresAt = &t
	}

	rec := types.MemoryRecord{
		ID:             uuid.NewString(),
		Namespace:      in.Namespace,
		Scope:          in.Scope,
		Content:        in.Content,
		Summary:        summary,
		Importance:     importance,
		SourceAgent:    in.SourceAgent,
		Metadata:       in.Metadata,
		CreatedAt:      now,
		LastAccessedAt: now,
		ExpiresAt:      expiresAt,
	}

	stored, err := s.store.InsertMemory(ctx, rec)
	if err != nil {
		return types.MemoryRecord{}, err
	}

	return stored, nil
}

// Search returns ranked memory items.
func (s *Service) Search(ctx context.Context, in types.SearchInput) ([]types.SearchResult, error) {
	if err := s.validateNamespace(in.Namespace); err != nil {
		return nil, err
	}
	in.Scope = strings.TrimSpace(strings.ToLower(in.Scope))
	if in.Scope != "" && in.Scope != "short" && in.Scope != "long" {
		return nil, fmt.Errorf("invalid scope %q", in.Scope)
	}
	if in.K <= 0 {
		in.K = s.cfg.DefaultSearchK
	}
	if in.K > 100 {
		in.K = 100
	}

	now := time.Now().UTC()
	cands, err := s.store.SearchCandidates(ctx, in.Namespace, in.Query, in.Scope, in.K*3, now)
	if err != nil {
		return nil, err
	}

	results := make([]types.SearchResult, 0, len(cands))
	for _, c := range cands {
		recency := recencyScore(now, c.Record.CreatedAt)
		importance := float64(c.Record.Importance) / 5.0
		score := (0.60 * c.LexicalScore) + (0.25 * recency) + (0.15 * importance)
		results = append(results, types.SearchResult{
			Record:          c.Record,
			Score:           score,
			LexicalScore:    c.LexicalScore,
			RecencyScore:    recency,
			ImportanceScore: importance,
		})
	}

	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if len(results) > in.K {
		results = results[:in.K]
	}

	if !in.IncludeMetadata {
		for i := range results {
			results[i].Record.Metadata = nil
		}
	}
	return results, nil
}

// ContextPack builds a compact context block bounded by token budget.
func (s *Service) ContextPack(ctx context.Context, in types.ContextPackInput) (types.ContextPack, error) {
	if in.TokenBudget <= 0 {
		in.TokenBudget = 512
	}
	if in.K <= 0 {
		in.K = s.cfg.MaxContextPackItems
	}
	if in.K > 50 {
		in.K = 50
	}

	results, err := s.Search(ctx, types.SearchInput{
		Namespace:       in.Namespace,
		Query:           in.Query,
		Scope:           in.Scope,
		K:               in.K,
		IncludeMetadata: false,
	})
	if err != nil {
		return types.ContextPack{}, err
	}

	seen := map[string]struct{}{}
	lines := make([]string, 0, len(results))
	ids := make([]string, 0, len(results))
	tokens := 0

	for _, r := range results {
		text := strings.TrimSpace(r.Record.Summary)
		if text == "" {
			text = strings.TrimSpace(r.Record.Content)
		}
		if text == "" {
			continue
		}

		norm := normalize(text)
		if _, ok := seen[norm]; ok {
			continue
		}
		seen[norm] = struct{}{}

		line := fmt.Sprintf("- [%s] %s", r.Record.ID, truncate(text, 300))
		lineTokens := estimateTokens(line)
		if tokens+lineTokens > in.TokenBudget {
			break
		}
		tokens += lineTokens
		lines = append(lines, line)
		ids = append(ids, r.Record.ID)
	}

	pack := types.ContextPack{
		Text:            strings.Join(lines, "\n"),
		EstimatedTokens: tokens,
		MemoryIDs:       ids,
	}
	return pack, nil
}

// Promote moves a memory to long-term scope.
func (s *Service) Promote(ctx context.Context, in types.PromoteInput) (types.MemoryRecord, error) {
	if strings.TrimSpace(in.MemoryID) == "" {
		return types.MemoryRecord{}, errors.New("memory_id is required")
	}
	if in.TargetScope == "" {
		in.TargetScope = "long"
	}
	if in.TargetScope != "long" {
		return types.MemoryRecord{}, errors.New("only target_scope=long is supported")
	}

	now := time.Now().UTC()
	if err := s.store.Promote(ctx, in.MemoryID, now); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return types.MemoryRecord{}, fmt.Errorf("memory %s not found", in.MemoryID)
		}
		return types.MemoryRecord{}, err
	}
	return s.store.GetMemory(ctx, in.MemoryID)
}

// ExpireShort triggers TTL cleanup.
func (s *Service) ExpireShort(ctx context.Context) (int64, error) {
	return s.store.ExpireShort(ctx, time.Now().UTC())
}

func (s *Service) validateNamespace(namespace string) error {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return errors.New("namespace is required")
	}
	if !s.namespaceExpr.MatchString(namespace) {
		return fmt.Errorf("namespace %q does not match required pattern", namespace)
	}
	return nil
}

func autoSummary(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	return truncate(content, 160)
}

func truncate(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	if limit < 3 {
		return string(r[:limit])
	}
	return string(r[:limit-3]) + "..."
}

func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Join(strings.Fields(s), " ")
	return truncate(s, 180)
}

func recencyScore(now, t time.Time) float64 {
	days := now.Sub(t).Hours() / 24.0
	if days <= 0 {
		return 1.0
	}
	return math.Exp(-days / 14.0)
}

// EstimateTokens is a rough approximation for prompt budgeting.
func estimateTokens(s string) int {
	runes := len([]rune(s))
	t := int(math.Ceil(float64(runes) / 4.0))
	if t < 1 {
		return 1
	}
	return t
}
