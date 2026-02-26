package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/log"
	_ "modernc.org/sqlite"

	"github.com/xiy/memory-mcp/pkg/types"
)

//go:embed schema.sql
var schemaSQL string

// Candidate is an unranked-ish store-level search candidate.
type Candidate struct {
	Record       types.MemoryRecord
	LexicalScore float64
}

// Stats summarizes database counters for admin dashboards.
type Stats struct {
	Total   int64
	Short   int64
	Long    int64
	Expired int64
}

// MCPRequestLog captures one incoming MCP request handled by the server.
type MCPRequestLog struct {
	ID         int64
	Method     string
	ToolName   string
	Success    bool
	ErrorText  string
	DurationMS int64
	CreatedAt  time.Time
}

// RecentMemory is a compact summary row for admin dashboards.
type RecentMemory struct {
	ID         string
	Namespace  string
	Scope      string
	Summary    string
	Importance int
	CreatedAt  time.Time
}

// Store represents persistence operations used by memory service.
type Store interface {
	InsertMemory(ctx context.Context, rec types.MemoryRecord) (types.MemoryRecord, error)
	SearchCandidates(ctx context.Context, namespace, query, scope string, limit int, now time.Time) ([]Candidate, error)
	Promote(ctx context.Context, id string, now time.Time) error
	ExpireShort(ctx context.Context, now time.Time) (int64, error)
	Stats(ctx context.Context, now time.Time) (Stats, error)
	GetMemory(ctx context.Context, id string) (types.MemoryRecord, error)
	Close() error
}

// SQLiteStore is a SQLite-backed memory store.
type SQLiteStore struct {
	db         *sql.DB
	logger     *log.Logger
	ftsEnabled bool
}

// OpenSQLite opens and initializes the SQLite store.
func OpenSQLite(ctx context.Context, dbPath string, logger *log.Logger) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir db dir: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	s := &SQLiteStore{db: db, logger: logger}
	if err := s.init(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLiteStore) init(ctx context.Context) error {
	for _, stmt := range splitSQLStatements(schemaSQL) {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			if strings.Contains(strings.ToLower(stmt), "virtual table") {
				s.logger.Warn("FTS5 disabled; falling back to LIKE queries", "error", err)
				s.ftsEnabled = false
				continue
			}
			return fmt.Errorf("run schema stmt: %w", err)
		}
	}

	s.ftsEnabled = s.hasFTSTable(ctx)
	return nil
}

func splitSQLStatements(s string) []string {
	parts := strings.Split(s, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p+";")
	}
	return out
}

func (s *SQLiteStore) hasFTSTable(ctx context.Context) bool {
	const q = `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='memories_fts'`
	var n int
	if err := s.db.QueryRowContext(ctx, q).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

func (s *SQLiteStore) InsertMemory(ctx context.Context, rec types.MemoryRecord) (types.MemoryRecord, error) {
	meta := rec.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return rec, fmt.Errorf("marshal metadata: %w", err)
	}

	expiresAt := sql.NullString{}
	if rec.ExpiresAt != nil {
		expiresAt = sql.NullString{String: rec.ExpiresAt.UTC().Format(time.RFC3339Nano), Valid: true}
	}
	promotedAt := sql.NullString{}
	if rec.PromotedAt != nil {
		promotedAt = sql.NullString{String: rec.PromotedAt.UTC().Format(time.RFC3339Nano), Valid: true}
	}

	const q = `INSERT INTO memories (
		id, namespace, scope, content, summary, importance, source_agent, metadata_json,
		created_at, last_accessed_at, expires_at, promoted_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err = s.db.ExecContext(ctx, q,
		rec.ID,
		rec.Namespace,
		rec.Scope,
		rec.Content,
		rec.Summary,
		rec.Importance,
		rec.SourceAgent,
		string(metaJSON),
		rec.CreatedAt.UTC().Format(time.RFC3339Nano),
		rec.LastAccessedAt.UTC().Format(time.RFC3339Nano),
		expiresAt,
		promotedAt,
	)
	if err != nil {
		return rec, fmt.Errorf("insert memory: %w", err)
	}

	if s.ftsEnabled {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO memories_fts(id, content, summary) VALUES (?, ?, ?)`,
			rec.ID, rec.Content, rec.Summary,
		); err != nil {
			s.logger.Warn("fts insert failed; continuing", "error", err)
		}
	}

	return rec, nil
}

func (s *SQLiteStore) SearchCandidates(ctx context.Context, namespace, query, scope string, limit int, now time.Time) ([]Candidate, error) {
	if limit <= 0 {
		limit = 10
	}
	query = strings.TrimSpace(query)
	terms := tokenizeQueryTerms(query)

	if len(terms) > 0 && s.ftsEnabled {
		ftsQuery := buildFTSMatchQuery(terms)
		rows, err := s.searchFTS(ctx, namespace, ftsQuery, scope, limit, now)
		if err == nil && len(rows) > 0 {
			return rows, nil
		}
		if err == nil && len(rows) == 0 {
			// Fallback to LIKE for edge cases where FTS tokenization misses expected matches.
			return s.searchLIKE(ctx, namespace, query, terms, scope, limit, now)
		}
		s.logger.Warn("fts query failed; fallback to LIKE", "error", err)
	}

	return s.searchLIKE(ctx, namespace, query, terms, scope, limit, now)
}

func (s *SQLiteStore) searchFTS(ctx context.Context, namespace, query, scope string, limit int, now time.Time) ([]Candidate, error) {
	base := `
SELECT m.id, m.namespace, m.scope, m.content, m.summary, m.importance, m.source_agent,
       m.metadata_json, m.created_at, m.last_accessed_at, m.expires_at, m.promoted_at,
       bm25(memories_fts) AS bm
FROM memories_fts
JOIN memories m ON m.id = memories_fts.id
WHERE memories_fts MATCH ?
  AND m.namespace = ?
  AND (m.expires_at IS NULL OR m.expires_at > ?)
`
	args := []any{query, namespace, now.UTC().Format(time.RFC3339Nano)}
	if scope != "" {
		base += " AND m.scope = ?\n"
		args = append(args, scope)
	}
	base += "ORDER BY bm ASC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, base, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]Candidate, 0, limit)
	for rows.Next() {
		rec, bm, err := scanCandidateRow(rows)
		if err != nil {
			return nil, err
		}
		lex := 1.0 / (1.0 + math.Abs(bm))
		items = append(items, Candidate{Record: rec, LexicalScore: lex})
	}
	return items, rows.Err()
}

func (s *SQLiteStore) searchLIKE(ctx context.Context, namespace, query string, terms []string, scope string, limit int, now time.Time) ([]Candidate, error) {
	base := `
SELECT id, namespace, scope, content, summary, importance, source_agent,
       metadata_json, created_at, last_accessed_at, expires_at, promoted_at
FROM memories
WHERE namespace = ?
  AND (expires_at IS NULL OR expires_at > ?)
`
	args := []any{namespace, now.UTC().Format(time.RFC3339Nano)}
	if scope != "" {
		base += " AND scope = ?\n"
		args = append(args, scope)
	}
	if len(terms) > 0 {
		for _, term := range terms {
			base += " AND (content LIKE ? OR summary LIKE ?)\n"
			needle := "%" + term + "%"
			args = append(args, needle, needle)
		}
	} else if query != "" {
		// If query had no extractable tokens (e.g. only punctuation), keep best-effort behavior.
		base += " AND (content LIKE ? OR summary LIKE ?)\n"
		needle := "%" + query + "%"
		args = append(args, needle, needle)
	}
	base += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, base, args...)
	if err != nil {
		return nil, fmt.Errorf("search like: %w", err)
	}
	defer rows.Close()

	items := make([]Candidate, 0, limit)
	for rows.Next() {
		rec, err := scanMemoryRow(rows)
		if err != nil {
			return nil, err
		}
		lex := 0.4
		if query == "" {
			lex = 0.25
		}
		items = append(items, Candidate{Record: rec, LexicalScore: lex})
	}
	return items, rows.Err()
}

func tokenizeQueryTerms(query string) []string {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	seen := map[string]struct{}{}
	terms := make([]string, 0, 6)
	var sb strings.Builder

	flush := func() {
		if sb.Len() == 0 {
			return
		}
		term := sb.String()
		sb.Reset()
		if _, ok := seen[term]; ok {
			return
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
	}

	for _, r := range query {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(unicode.ToLower(r))
			continue
		}
		flush()
	}
	flush()
	return terms
}

func buildFTSMatchQuery(terms []string) string {
	if len(terms) == 0 {
		return ""
	}
	parts := make([]string, 0, len(terms))
	for _, term := range terms {
		escaped := strings.ReplaceAll(term, `"`, `""`)
		parts = append(parts, `"`+escaped+`"`)
	}
	return strings.Join(parts, " AND ")
}

func (s *SQLiteStore) Promote(ctx context.Context, id string, now time.Time) error {
	const q = `UPDATE memories
SET scope = 'long', expires_at = NULL, promoted_at = ?, last_accessed_at = ?
WHERE id = ?`
	res, err := s.db.ExecContext(ctx, q, now.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return fmt.Errorf("promote memory: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("promote rows affected: %w", err)
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) ExpireShort(ctx context.Context, now time.Time) (int64, error) {
	const q = `DELETE FROM memories WHERE scope = 'short' AND expires_at IS NOT NULL AND expires_at <= ?`
	res, err := s.db.ExecContext(ctx, q, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("expire short memories: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("expire rows affected: %w", err)
	}
	if s.ftsEnabled && n > 0 {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM memories_fts WHERE id NOT IN (SELECT id FROM memories)`)
	}
	return n, nil
}

func (s *SQLiteStore) Stats(ctx context.Context, now time.Time) (Stats, error) {
	var st Stats
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM memories`).Scan(&st.Total); err != nil {
		return st, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM memories WHERE scope = 'short'`).Scan(&st.Short); err != nil {
		return st, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM memories WHERE scope = 'long'`).Scan(&st.Long); err != nil {
		return st, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM memories WHERE expires_at IS NOT NULL AND expires_at <= ?`, now.UTC().Format(time.RFC3339Nano)).Scan(&st.Expired); err != nil {
		return st, err
	}
	return st, nil
}

// InsertMCPRequestLog stores one request event for admin observability.
func (s *SQLiteStore) InsertMCPRequestLog(ctx context.Context, rec MCPRequestLog) error {
	ts := rec.CreatedAt.UTC()
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	success := 0
	if rec.Success {
		success = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO mcp_requests (
		method, tool_name, success, error_text, duration_ms, created_at
	) VALUES (?, ?, ?, ?, ?, ?)`,
		strings.TrimSpace(rec.Method),
		strings.TrimSpace(rec.ToolName),
		success,
		strings.TrimSpace(rec.ErrorText),
		rec.DurationMS,
		ts.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert mcp request log: %w", err)
	}
	return nil
}

// RecentMCPRequestLogs returns most recent request events in newest-first order.
func (s *SQLiteStore) RecentMCPRequestLogs(ctx context.Context, limit int) ([]MCPRequestLog, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, method, tool_name, success, error_text, duration_ms, created_at
FROM mcp_requests
ORDER BY created_at DESC
LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list mcp request logs: %w", err)
	}
	defer rows.Close()

	items := make([]MCPRequestLog, 0, limit)
	for rows.Next() {
		var (
			row            MCPRequestLog
			successAsInt   int
			createdAtValue string
		)
		if err := rows.Scan(
			&row.ID,
			&row.Method,
			&row.ToolName,
			&successAsInt,
			&row.ErrorText,
			&row.DurationMS,
			&createdAtValue,
		); err != nil {
			return nil, fmt.Errorf("scan mcp request log: %w", err)
		}
		row.Success = successAsInt == 1
		if ts, err := time.Parse(time.RFC3339Nano, createdAtValue); err == nil {
			row.CreatedAt = ts
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// RecentMemories returns compact memory rows in newest-first order.
func (s *SQLiteStore) RecentMemories(ctx context.Context, limit int) ([]RecentMemory, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, namespace, scope, summary, content, importance, created_at
FROM memories
ORDER BY created_at DESC
LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent memories: %w", err)
	}
	defer rows.Close()

	items := make([]RecentMemory, 0, limit)
	for rows.Next() {
		var (
			row            RecentMemory
			content        string
			createdAtValue string
		)
		if err := rows.Scan(
			&row.ID,
			&row.Namespace,
			&row.Scope,
			&row.Summary,
			&content,
			&row.Importance,
			&createdAtValue,
		); err != nil {
			return nil, fmt.Errorf("scan recent memory: %w", err)
		}
		if strings.TrimSpace(row.Summary) == "" {
			row.Summary = content
		}
		if ts, err := time.Parse(time.RFC3339Nano, createdAtValue); err == nil {
			row.CreatedAt = ts
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) GetMemory(ctx context.Context, id string) (types.MemoryRecord, error) {
	const q = `SELECT id, namespace, scope, content, summary, importance, source_agent,
       metadata_json, created_at, last_accessed_at, expires_at, promoted_at
FROM memories WHERE id = ? LIMIT 1`
	row := s.db.QueryRowContext(ctx, q, id)
	rec, err := scanMemoryRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rec, err
		}
		return rec, fmt.Errorf("get memory: %w", err)
	}
	return rec, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanCandidateRow(sc scanner) (types.MemoryRecord, float64, error) {
	rec, err := scanBaseMemory(sc, true)
	if err != nil {
		return types.MemoryRecord{}, 0, err
	}
	bm := rec.Metadata["_bm25"].(float64)
	delete(rec.Metadata, "_bm25")
	return rec, bm, nil
}

func scanMemoryRow(sc scanner) (types.MemoryRecord, error) {
	rec, err := scanBaseMemory(sc, false)
	return rec, err
}

func scanBaseMemory(sc scanner, withBM25 bool) (types.MemoryRecord, error) {
	var rec types.MemoryRecord
	var metadataJSON string
	var createdAt, lastAccessedAt string
	var expiresAt, promotedAt sql.NullString
	if withBM25 {
		var bm float64
		err := sc.Scan(
			&rec.ID,
			&rec.Namespace,
			&rec.Scope,
			&rec.Content,
			&rec.Summary,
			&rec.Importance,
			&rec.SourceAgent,
			&metadataJSON,
			&createdAt,
			&lastAccessedAt,
			&expiresAt,
			&promotedAt,
			&bm,
		)
		if err != nil {
			return rec, err
		}
		rec.Metadata = map[string]any{"_bm25": bm}
	} else {
		err := sc.Scan(
			&rec.ID,
			&rec.Namespace,
			&rec.Scope,
			&rec.Content,
			&rec.Summary,
			&rec.Importance,
			&rec.SourceAgent,
			&metadataJSON,
			&createdAt,
			&lastAccessedAt,
			&expiresAt,
			&promotedAt,
		)
		if err != nil {
			return rec, err
		}
	}

	if err := json.Unmarshal([]byte(metadataJSON), &rec.Metadata); err != nil {
		rec.Metadata = map[string]any{}
	}

	created, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return rec, err
	}
	last, err := time.Parse(time.RFC3339Nano, lastAccessedAt)
	if err != nil {
		return rec, err
	}
	rec.CreatedAt = created
	rec.LastAccessedAt = last

	if expiresAt.Valid {
		t, err := time.Parse(time.RFC3339Nano, expiresAt.String)
		if err == nil {
			rec.ExpiresAt = &t
		}
	}
	if promotedAt.Valid {
		t, err := time.Parse(time.RFC3339Nano, promotedAt.String)
		if err == nil {
			rec.PromotedAt = &t
		}
	}
	return rec, nil
}
