PRAGMA journal_mode=WAL;
PRAGMA synchronous=NORMAL;

CREATE TABLE IF NOT EXISTS memories (
  id TEXT PRIMARY KEY,
  namespace TEXT NOT NULL,
  scope TEXT NOT NULL CHECK(scope IN ('short', 'long')),
  content TEXT NOT NULL,
  summary TEXT NOT NULL DEFAULT '',
  importance INTEGER NOT NULL DEFAULT 3,
  source_agent TEXT NOT NULL DEFAULT '',
  metadata_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  last_accessed_at TEXT NOT NULL,
  expires_at TEXT,
  promoted_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_memories_namespace_scope ON memories(namespace, scope);
CREATE INDEX IF NOT EXISTS idx_memories_expires_at ON memories(expires_at);
CREATE INDEX IF NOT EXISTS idx_memories_created_at ON memories(created_at DESC);

CREATE TABLE IF NOT EXISTS mcp_requests (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  method TEXT NOT NULL,
  tool_name TEXT NOT NULL DEFAULT '',
  success INTEGER NOT NULL,
  error_text TEXT NOT NULL DEFAULT '',
  duration_ms INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_mcp_requests_created_at ON mcp_requests(created_at DESC);

-- FTS5 table for lexical retrieval. We keep a separate id column for joins.
CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
  id UNINDEXED,
  content,
  summary
);
