package memory

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	"github.com/xiy/memory-mcp/internal/config"
	"github.com/xiy/memory-mcp/internal/store"
	"github.com/xiy/memory-mcp/pkg/types"
)

type fakeStore struct {
	inserted []types.MemoryRecord
	search   []store.Candidate
}

func (f *fakeStore) InsertMemory(_ context.Context, rec types.MemoryRecord) (types.MemoryRecord, error) {
	f.inserted = append(f.inserted, rec)
	return rec, nil
}
func (f *fakeStore) SearchCandidates(_ context.Context, _, _, _ string, _ int, _ time.Time) ([]store.Candidate, error) {
	return f.search, nil
}
func (f *fakeStore) Promote(_ context.Context, _ string, _ time.Time) error    { return nil }
func (f *fakeStore) ExpireShort(_ context.Context, _ time.Time) (int64, error) { return 0, nil }
func (f *fakeStore) Stats(_ context.Context, _ time.Time) (store.Stats, error) {
	return store.Stats{}, nil
}
func (f *fakeStore) GetMemory(_ context.Context, _ string) (types.MemoryRecord, error) {
	if len(f.inserted) == 0 {
		return types.MemoryRecord{}, nil
	}
	return f.inserted[0], nil
}
func (f *fakeStore) Close() error { return nil }

func TestWrite_ValidatesNamespace(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	logger := log.NewWithOptions(io.Discard, log.Options{})
	svc, err := NewService(&fakeStore{}, cfg, logger)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	_, err = svc.Write(context.Background(), types.WriteInput{
		Namespace: "bad namespace",
		Content:   "hello",
	})
	if err == nil {
		t.Fatal("expected namespace validation error, got nil")
	}
}

func TestContextPack_RespectsTokenBudget(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	st := &fakeStore{search: []store.Candidate{
		{Record: types.MemoryRecord{ID: "a", Summary: "first long summary entry", CreatedAt: now, Importance: 5}, LexicalScore: 0.9},
		{Record: types.MemoryRecord{ID: "b", Summary: "second long summary entry", CreatedAt: now, Importance: 5}, LexicalScore: 0.9},
	}}
	cfg := config.Default()
	logger := log.NewWithOptions(io.Discard, log.Options{})
	svc, err := NewService(st, cfg, logger)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	pack, err := svc.ContextPack(context.Background(), types.ContextPackInput{
		Namespace:   "org/repo/task",
		Query:       "summary",
		TokenBudget: 5,
		K:           10,
	})
	if err != nil {
		t.Fatalf("ContextPack() error = %v", err)
	}
	if pack.EstimatedTokens > 5 {
		t.Fatalf("expected token budget <= 5, got %d", pack.EstimatedTokens)
	}
}
