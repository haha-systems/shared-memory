package store

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	"github.com/xiy/memory-mcp/pkg/types"
)

func TestSQLiteStore_RoundTripAndExpire(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	logger := log.NewWithOptions(io.Discard, log.Options{})
	dbPath := filepath.Join(t.TempDir(), "memories.db")

	st, err := OpenSQLite(ctx, dbPath, logger)
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	expiresSoon := now.Add(2 * time.Second)
	short := types.MemoryRecord{
		ID:             "m-short",
		Namespace:      "org/repo/task",
		Scope:          "short",
		Content:        "remember alpha deployment issue",
		Summary:        "alpha deployment issue",
		Importance:     4,
		CreatedAt:      now,
		LastAccessedAt: now,
		ExpiresAt:      &expiresSoon,
	}
	if _, err := st.InsertMemory(ctx, short); err != nil {
		t.Fatalf("InsertMemory(short) error = %v", err)
	}

	long := types.MemoryRecord{
		ID:             "m-long",
		Namespace:      "org/repo/task",
		Scope:          "long",
		Content:        "long term design decision",
		Summary:        "design decision",
		Importance:     5,
		CreatedAt:      now,
		LastAccessedAt: now,
	}
	if _, err := st.InsertMemory(ctx, long); err != nil {
		t.Fatalf("InsertMemory(long) error = %v", err)
	}

	cands, err := st.SearchCandidates(ctx, "org/repo/task", "deployment", "", 10, now)
	if err != nil {
		t.Fatalf("SearchCandidates() error = %v", err)
	}
	if len(cands) == 0 {
		t.Fatalf("expected search candidates, got 0")
	}

	hyphenRec := types.MemoryRecord{
		ID:             "m-hyphen",
		Namespace:      "org/repo/task",
		Scope:          "short",
		Content:        "shared-memory startup verification completed",
		Summary:        "shared-memory verification",
		Importance:     5,
		CreatedAt:      now,
		LastAccessedAt: now,
		ExpiresAt:      &expiresSoon,
	}
	if _, err := st.InsertMemory(ctx, hyphenRec); err != nil {
		t.Fatalf("InsertMemory(hyphenRec) error = %v", err)
	}

	hyphenCands, err := st.SearchCandidates(ctx, "org/repo/task", "shared-memory verification", "", 10, now)
	if err != nil {
		t.Fatalf("SearchCandidates(hyphen query) error = %v", err)
	}
	if len(hyphenCands) == 0 {
		t.Fatalf("expected hyphen query to return results, got 0")
	}

	if err := st.Promote(ctx, "m-short", now); err != nil {
		t.Fatalf("Promote() error = %v", err)
	}
	got, err := st.GetMemory(ctx, "m-short")
	if err != nil {
		t.Fatalf("GetMemory() error = %v", err)
	}
	if got.Scope != "long" {
		t.Fatalf("expected promoted scope long, got %q", got.Scope)
	}

	// Create an expired short memory and ensure cleanup removes it.
	expired := types.MemoryRecord{
		ID:             "m-expired",
		Namespace:      "org/repo/task",
		Scope:          "short",
		Content:        "stale memory",
		Summary:        "stale",
		Importance:     2,
		CreatedAt:      now,
		LastAccessedAt: now,
	}
	past := now.Add(-1 * time.Hour)
	expired.ExpiresAt = &past
	if _, err := st.InsertMemory(ctx, expired); err != nil {
		t.Fatalf("InsertMemory(expired) error = %v", err)
	}

	n, err := st.ExpireShort(ctx, now)
	if err != nil {
		t.Fatalf("ExpireShort() error = %v", err)
	}
	if n < 1 {
		t.Fatalf("expected at least one expired row removed, got %d", n)
	}
}

func TestSQLiteStore_RequestLogsAndRecentMemories(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	logger := log.NewWithOptions(io.Discard, log.Options{})
	dbPath := filepath.Join(t.TempDir(), "memories.db")

	st, err := OpenSQLite(ctx, dbPath, logger)
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	defer st.Close()

	base := time.Date(2026, 2, 17, 10, 0, 0, 0, time.UTC)
	recOld := types.MemoryRecord{
		ID:             "m-old",
		Namespace:      "org/repo/task",
		Scope:          "short",
		Content:        "older memory content",
		Summary:        "older summary",
		Importance:     2,
		CreatedAt:      base.Add(-2 * time.Minute),
		LastAccessedAt: base.Add(-2 * time.Minute),
	}
	if _, err := st.InsertMemory(ctx, recOld); err != nil {
		t.Fatalf("InsertMemory(recOld) error = %v", err)
	}

	recNew := types.MemoryRecord{
		ID:             "m-new",
		Namespace:      "org/repo/task",
		Scope:          "long",
		Content:        "newest memory content",
		Summary:        "",
		Importance:     5,
		CreatedAt:      base,
		LastAccessedAt: base,
	}
	if _, err := st.InsertMemory(ctx, recNew); err != nil {
		t.Fatalf("InsertMemory(recNew) error = %v", err)
	}

	if err := st.InsertMCPRequestLog(ctx, MCPRequestLog{
		Method:     "initialize",
		Success:    true,
		DurationMS: 2,
		CreatedAt:  base.Add(-1 * time.Minute),
	}); err != nil {
		t.Fatalf("InsertMCPRequestLog(initialize) error = %v", err)
	}
	if err := st.InsertMCPRequestLog(ctx, MCPRequestLog{
		Method:     "tools/call",
		ToolName:   "memory_search",
		Success:    false,
		ErrorText:  "namespace is required",
		DurationMS: 11,
		CreatedAt:  base,
	}); err != nil {
		t.Fatalf("InsertMCPRequestLog(tools/call) error = %v", err)
	}

	logs, err := st.RecentMCPRequestLogs(ctx, 5)
	if err != nil {
		t.Fatalf("RecentMCPRequestLogs() error = %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("expected 2 request logs, got %d", len(logs))
	}
	if logs[0].Method != "tools/call" || logs[0].ToolName != "memory_search" {
		t.Fatalf("expected newest request to be tools/call memory_search, got %+v", logs[0])
	}
	if logs[0].Success {
		t.Fatalf("expected newest request success=false, got true")
	}

	recent, err := st.RecentMemories(ctx, 5)
	if err != nil {
		t.Fatalf("RecentMemories() error = %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("expected 2 recent memories, got %d", len(recent))
	}
	if recent[0].ID != "m-new" {
		t.Fatalf("expected most recent memory m-new, got %q", recent[0].ID)
	}
	if recent[0].Summary != "newest memory content" {
		t.Fatalf("expected summary fallback from content, got %q", recent[0].Summary)
	}
}
