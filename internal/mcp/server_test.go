package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	"github.com/xiy/memory-mcp/internal/config"
	"github.com/xiy/memory-mcp/internal/memory"
	"github.com/xiy/memory-mcp/internal/store"
	"github.com/xiy/memory-mcp/pkg/types"
)

type fakeStore struct{}

func (fakeStore) InsertMemory(_ context.Context, rec types.MemoryRecord) (types.MemoryRecord, error) {
	return rec, nil
}
func (fakeStore) SearchCandidates(_ context.Context, _, _, _ string, _ int, _ time.Time) ([]store.Candidate, error) {
	return nil, nil
}
func (fakeStore) Promote(_ context.Context, _ string, _ time.Time) error    { return nil }
func (fakeStore) ExpireShort(_ context.Context, _ time.Time) (int64, error) { return 0, nil }
func (fakeStore) Stats(_ context.Context, _ time.Time) (store.Stats, error) {
	return store.Stats{}, nil
}
func (fakeStore) GetMemory(_ context.Context, id string) (types.MemoryRecord, error) {
	return types.MemoryRecord{ID: id, Namespace: "org/repo/task", Scope: "long"}, nil
}
func (fakeStore) Close() error { return nil }

type captureSink struct {
	rows []store.MCPRequestLog
}

func (c *captureSink) InsertMCPRequestLog(_ context.Context, rec store.MCPRequestLog) error {
	c.rows = append(c.rows, rec)
	return nil
}

func TestHandle_ToolsList(t *testing.T) {
	t.Parallel()
	svc, err := memory.NewService(fakeStore{}, config.Default(), log.NewWithOptions(io.Discard, log.Options{}))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	srv := NewServer(svc, log.NewWithOptions(io.Discard, log.Options{}), nil)

	id := json.RawMessage(`1`)
	resp, ok := srv.handle(context.Background(), request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "tools/list",
	})
	if !ok {
		t.Fatal("expected response")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error response: %+v", resp.Error)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type %T", resp.Result)
	}
	tools, ok := result["tools"].([]ToolDefinition)
	if !ok || len(tools) == 0 {
		t.Fatalf("expected non-empty tools list")
	}
}

func TestReadWriteFramedMessage(t *testing.T) {
	t.Parallel()
	resp := response{JSONRPC: "2.0", ID: 1, Result: map[string]any{"ok": true}}
	var payloadBuf bytes.Buffer
	bw := bufio.NewWriter(&payloadBuf)
	if err := writeFramedMessage(bw, resp); err != nil {
		t.Fatalf("writeFramedMessage() error = %v", err)
	}
	br := bufio.NewReader(bytes.NewReader(payloadBuf.Bytes()))
	payload, err := readFramedMessage(br)
	if err != nil {
		t.Fatalf("readFramedMessage() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got["jsonrpc"] != "2.0" {
		t.Fatalf("expected jsonrpc 2.0, got %v", got["jsonrpc"])
	}
}

func TestReadMessage_JSONLine(t *testing.T) {
	t.Parallel()
	raw := []byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}\n")
	br := bufio.NewReader(bytes.NewReader(raw))

	payload, mode, err := readMessage(br)
	if err != nil {
		t.Fatalf("readMessage() error = %v", err)
	}
	if mode != wireModeJSONLine {
		t.Fatalf("expected JSON-line mode, got %v", mode)
	}

	var req request
	if err := json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("json.Unmarshal(payload) error = %v", err)
	}
	if req.Method != "ping" {
		t.Fatalf("expected method ping, got %q", req.Method)
	}
}

func TestServe_JSONLineInitialize(t *testing.T) {
	t.Parallel()
	svc, err := memory.NewService(fakeStore{}, config.Default(), log.NewWithOptions(io.Discard, log.Options{}))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	srv := NewServer(svc, log.NewWithOptions(io.Discard, log.Options{}), nil)

	in := bytes.NewBufferString("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2024-11-05\"}}\n")
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}

	line := bytes.TrimSpace(out.Bytes())
	if len(line) == 0 {
		t.Fatal("expected JSON-line response, got empty output")
	}
	if bytes.Contains(line, []byte("Content-Length:")) {
		t.Fatalf("expected JSON-line response, got framed output: %q", string(line))
	}

	var resp map[string]any
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("json.Unmarshal(response) error = %v", err)
	}
	if resp["jsonrpc"] != "2.0" {
		t.Fatalf("expected jsonrpc 2.0, got %v", resp["jsonrpc"])
	}
}

func TestServe_LogsRequestEvents(t *testing.T) {
	t.Parallel()
	svc, err := memory.NewService(fakeStore{}, config.Default(), log.NewWithOptions(io.Discard, log.Options{}))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	sink := &captureSink{}
	srv := NewServer(svc, log.NewWithOptions(io.Discard, log.Options{}), sink)

	in := bytes.NewBufferString("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"memory_search\",\"arguments\":{\"query\":\"deploy\"}}}\n")
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}

	if len(sink.rows) != 1 {
		t.Fatalf("expected 1 request log row, got %d", len(sink.rows))
	}
	got := sink.rows[0]
	if got.Method != "tools/call" {
		t.Fatalf("expected method tools/call, got %q", got.Method)
	}
	if got.ToolName != "memory_search" {
		t.Fatalf("expected tool memory_search, got %q", got.ToolName)
	}
	if got.Success {
		t.Fatalf("expected failed request due to missing namespace")
	}
	if got.ErrorText == "" {
		t.Fatalf("expected non-empty error text")
	}
}
