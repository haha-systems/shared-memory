package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/charmbracelet/log"

	"github.com/xiy/memory-mcp/internal/memory"
	"github.com/xiy/memory-mcp/internal/store"
	"github.com/xiy/memory-mcp/pkg/types"
)

const jsonRPCVersion = "2.0"

// Server handles MCP JSON-RPC messages over stdio.
type Server struct {
	svc    *memory.Service
	logger *log.Logger
	sink   RequestLogSink

	requests uint64
	errors   uint64
}

// RequestLogSink receives summarized MCP request events.
type RequestLogSink interface {
	InsertMCPRequestLog(ctx context.Context, rec store.MCPRequestLog) error
}

// NewServer creates an MCP server.
func NewServer(svc *memory.Service, logger *log.Logger, sink RequestLogSink) *Server {
	return &Server{svc: svc, logger: logger, sink: sink}
}

// Serve starts MCP handling over the provided streams.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	br := bufio.NewReader(in)
	bw := bufio.NewWriter(out)
	defer bw.Flush()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		payload, mode, err := readMessage(br)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		var req request
		if err := json.Unmarshal(payload, &req); err != nil {
			s.logger.Warn("invalid JSON-RPC request", "error", err)
			s.recordRequest(ctx, request{Method: "parse_error"}, response{
				Error: &rpcError{
					Code:    -32700,
					Message: "parse error",
					Data:    err.Error(),
				},
			}, 0)
			resp := errorResponse(nil, -32700, "parse error", err.Error())
			if werr := writeFramedMessage(bw, resp); werr != nil {
				return werr
			}
			continue
		}

		started := time.Now()
		resp, shouldRespond := s.handle(ctx, req)
		s.recordRequest(ctx, req, resp, time.Since(started))
		if !shouldRespond {
			continue
		}
		if err := writeMessage(bw, resp, mode); err != nil {
			return err
		}
	}
}

type wireMode int

const (
	wireModeFramed wireMode = iota
	wireModeJSONLine
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func (s *Server) handle(ctx context.Context, req request) (response, bool) {
	atomic.AddUint64(&s.requests, 1)

	hasID := len(req.ID) > 0
	id := decodeID(req.ID)

	if req.Method == "notifications/initialized" {
		return response{}, false
	}

	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		pv := p.ProtocolVersion
		if strings.TrimSpace(pv) == "" {
			pv = "2024-11-05"
		}
		return response{JSONRPC: jsonRPCVersion, ID: id, Result: map[string]any{
			"protocolVersion": pv,
			"capabilities": map[string]any{
				"tools": map[string]any{
					"listChanged": false,
				},
			},
			"serverInfo": map[string]any{
				"name":    "memory-mcp",
				"version": "0.1.0",
			},
		}}, hasID
	case "ping":
		return response{JSONRPC: jsonRPCVersion, ID: id, Result: map[string]any{}}, hasID
	case "tools/list":
		defs := toolDefinitions()
		return response{JSONRPC: jsonRPCVersion, ID: id, Result: map[string]any{"tools": defs}}, hasID
	case "tools/call":
		res, err := s.handleToolCall(ctx, req.Params)
		if err != nil {
			atomic.AddUint64(&s.errors, 1)
			return response{JSONRPC: jsonRPCVersion, ID: id, Result: map[string]any{
				"content": []map[string]any{{"type": "text", "text": err.Error()}},
				"isError": true,
			}}, hasID
		}
		return response{JSONRPC: jsonRPCVersion, ID: id, Result: res}, hasID
	default:
		if !hasID {
			return response{}, false
		}
		return errorResponse(id, -32601, "method not found", req.Method), true
	}
}

func (s *Server) recordRequest(ctx context.Context, req request, resp response, duration time.Duration) {
	if s.sink == nil {
		return
	}
	rec := store.MCPRequestLog{
		Method:     strings.TrimSpace(req.Method),
		ToolName:   toolNameFromParams(req.Method, req.Params),
		Success:    responseSuccessful(resp),
		ErrorText:  responseErrorText(resp),
		DurationMS: duration.Milliseconds(),
		CreatedAt:  time.Now().UTC(),
	}
	if strings.TrimSpace(rec.Method) == "" {
		rec.Method = "unknown"
	}
	if err := s.sink.InsertMCPRequestLog(ctx, rec); err != nil {
		s.logger.Warn("failed to persist MCP request log", "error", err)
	}
}

func toolNameFromParams(method string, params json.RawMessage) string {
	if method != "tools/call" || len(params) == 0 {
		return ""
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(params, &in); err != nil {
		return ""
	}
	return strings.TrimSpace(in.Name)
}

func responseSuccessful(resp response) bool {
	if resp.Error != nil {
		return false
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		return true
	}
	isError, ok := result["isError"].(bool)
	if !ok {
		return true
	}
	return !isError
}

func responseErrorText(resp response) string {
	if resp.Error != nil {
		return strings.TrimSpace(resp.Error.Message)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		return ""
	}
	isError, ok := result["isError"].(bool)
	if !ok || !isError {
		return ""
	}
	content, ok := result["content"].([]map[string]any)
	if !ok || len(content) == 0 {
		return "tool call failed"
	}
	text, _ := content[0]["text"].(string)
	text = strings.TrimSpace(text)
	if text == "" {
		return "tool call failed"
	}
	return text
}

func (s *Server) handleToolCall(ctx context.Context, params json.RawMessage) (map[string]any, error) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid tools/call params: %w", err)
	}

	switch p.Name {
	case "memory_write":
		var in types.WriteInput
		if err := json.Unmarshal(p.Arguments, &in); err != nil {
			return nil, fmt.Errorf("invalid memory_write arguments: %w", err)
		}
		rec, err := s.svc.Write(ctx, in)
		if err != nil {
			return nil, err
		}
		return toolSuccess(rec)
	case "memory_search":
		var in types.SearchInput
		if err := json.Unmarshal(p.Arguments, &in); err != nil {
			return nil, fmt.Errorf("invalid memory_search arguments: %w", err)
		}
		items, err := s.svc.Search(ctx, in)
		if err != nil {
			return nil, err
		}
		return toolSuccess(items)
	case "memory_get_context_pack":
		var in types.ContextPackInput
		if err := json.Unmarshal(p.Arguments, &in); err != nil {
			return nil, fmt.Errorf("invalid memory_get_context_pack arguments: %w", err)
		}
		pack, err := s.svc.ContextPack(ctx, in)
		if err != nil {
			return nil, err
		}
		return toolSuccess(pack)
	case "memory_promote":
		var in types.PromoteInput
		if err := json.Unmarshal(p.Arguments, &in); err != nil {
			return nil, fmt.Errorf("invalid memory_promote arguments: %w", err)
		}
		rec, err := s.svc.Promote(ctx, in)
		if err != nil {
			return nil, err
		}
		return toolSuccess(rec)
	default:
		return nil, fmt.Errorf("unknown tool %q", p.Name)
	}
}

func toolSuccess(v any) (map[string]any, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(b)}},
		"structuredContent": v,
		"isError":           false,
	}, nil
}

func errorResponse(id interface{}, code int, msg string, data interface{}) response {
	return response{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Error: &rpcError{
			Code:    code,
			Message: msg,
			Data:    data,
		},
	}
}

func decodeID(raw json.RawMessage) interface{} {
	if len(raw) == 0 {
		return nil
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	return v
}

func writeFramedMessage(w *bufio.Writer, msg response) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(payload))
	if _, err := w.WriteString(header); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	return w.Flush()
}

func writeMessage(w *bufio.Writer, msg response, mode wireMode) error {
	if mode == wireModeJSONLine {
		payload, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		if _, err := w.Write(payload); err != nil {
			return err
		}
		if err := w.WriteByte('\n'); err != nil {
			return err
		}
		return w.Flush()
	}
	return writeFramedMessage(w, msg)
}

func readMessage(r *bufio.Reader) ([]byte, wireMode, error) {
	mode, err := detectWireMode(r)
	if err != nil {
		return nil, wireModeFramed, err
	}
	if mode == wireModeJSONLine {
		return readJSONLineMessage(r)
	}
	payload, err := readFramedMessage(r)
	return payload, wireModeFramed, err
}

func detectWireMode(r *bufio.Reader) (wireMode, error) {
	for {
		b, err := r.Peek(1)
		if err != nil {
			return wireModeFramed, err
		}
		if !unicode.IsSpace(rune(b[0])) {
			break
		}
		_, _ = r.ReadByte()
	}

	peek, err := r.Peek(16)
	if err != nil && !errors.Is(err, bufio.ErrBufferFull) && !errors.Is(err, io.EOF) {
		return wireModeFramed, err
	}
	peekLower := strings.ToLower(string(peek))
	if strings.HasPrefix(peekLower, "content-length:") {
		return wireModeFramed, nil
	}
	return wireModeJSONLine, nil
}

func readJSONLineMessage(r *bufio.Reader) ([]byte, wireMode, error) {
	line, err := r.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, wireModeJSONLine, err
	}
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		if errors.Is(err, io.EOF) {
			return nil, wireModeJSONLine, io.EOF
		}
		return readJSONLineMessage(r)
	}
	return line, wireModeJSONLine, nil
}

func readFramedMessage(r *bufio.Reader) ([]byte, error) {
	contentLength := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(parts[0]), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil {
				return nil, fmt.Errorf("invalid Content-Length: %w", err)
			}
			contentLength = n
		}
	}
	if contentLength <= 0 {
		return nil, fmt.Errorf("missing or invalid Content-Length")
	}

	buf := make([]byte, contentLength)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// Snapshot returns server counters for dashboards.
func (s *Server) Snapshot() map[string]any {
	return map[string]any{
		"requests": atomic.LoadUint64(&s.requests),
		"errors":   atomic.LoadUint64(&s.errors),
		"ts":       time.Now().UTC(),
	}
}
