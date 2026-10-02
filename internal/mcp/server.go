// Package mcp speaks the Model Context Protocol over standard input and
// output. MCP is a standard way for an AI agent to discover and call tools
// that live in another program; messages are JSON-RPC 2.0, one per line.
//
// This package only knows the protocol. It does not know about Freshdesk.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"freshdesk-mcp-connector/internal/audit"
)

// Protocol version we answer with if the client asks for one we do not know.
const defaultProtocolVersion = "2025-06-18"

var supportedVersions = map[string]bool{"2024-11-05": true, "2025-03-26": true, "2025-06-18": true}

// ToolError is a failure the agent should see as tool output (isError),
// not as a protocol error.
type ToolError struct {
	Kind    string // short machine-readable category, written to the audit log
	Message string // plain sentence for the agent
}

func (e *ToolError) Error() string { return e.Message }

// Tool is one callable function exposed to the agent.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any // JSON Schema for the arguments
	Handler     func(ctx context.Context, args json.RawMessage) (string, error)
}

// Definition is the JSON shape of a tool in tools/list.
type Definition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations Annotations    `json:"annotations"`
}

// Annotations are hints to the agent host. Every tool here only reads.
type Annotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    bool   `json:"readOnlyHint"`
	DestructiveHint bool   `json:"destructiveHint"`
	IdempotentHint  bool   `json:"idempotentHint"`
	OpenWorldHint   bool   `json:"openWorldHint"`
}

// Definitions lists tools in the form clients expect.
func Definitions(tools []Tool) []Definition {
	defs := make([]Definition, len(tools))
	for i, t := range tools {
		defs[i] = Definition{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema,
			Annotations: Annotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: true}}
	}
	return defs
}

type Server struct {
	Name, Version string
	Instructions  string
	Tools         []Tool
	Audit         *audit.Logger // optional
	// MaxResultBytes is a last safety net: a tool result larger than this is
	// replaced by an error. Tools should already stay well below it.
	MaxResultBytes int
	Now            func() time.Time // defaults to time.Now
}

// maxLineBytes bounds a single incoming message.
const maxLineBytes = 4 << 20

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// JSON-RPC error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// Serve reads requests from r and writes responses to w until r ends or ctx
// is cancelled. Requests are handled one at a time, in order.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), maxLineBytes)
	enc := json.NewEncoder(w)
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := sc.Bytes()
		if len(trimSpace(line)) == 0 {
			continue
		}
		if resp := s.handleLine(ctx, line); resp != nil {
			if err := enc.Encode(resp); err != nil {
				return err
			}
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("reading input: %w", err)
	}
	return nil
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t' || b[0] == '\r') {
		b = b[1:]
	}
	return b
}

// handleLine returns nil for notifications, which get no reply.
func (s *Server) handleLine(ctx context.Context, line []byte) *response {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return errResp(json.RawMessage("null"), codeParse, "could not parse JSON")
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		return errResp(idOrNull(req.ID), codeInvalidRequest, "not a JSON-RPC 2.0 request")
	}
	isNotification := len(req.ID) == 0
	result, rpcErr := s.dispatch(ctx, req)
	if isNotification {
		return nil
	}
	if rpcErr != nil {
		return &response{JSONRPC: "2.0", ID: req.ID, Error: rpcErr}
	}
	return &response{JSONRPC: "2.0", ID: req.ID, Result: result}
}

func (s *Server) dispatch(ctx context.Context, req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return s.initialize(req.Params), nil
	case "notifications/initialized", "notifications/cancelled":
		return nil, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": Definitions(s.Tools)}, nil
	case "tools/call":
		return s.callTool(ctx, req.Params)
	}
	return nil, &rpcError{codeMethodNotFound, "method not found: " + req.Method}
}

func (s *Server) initialize(params json.RawMessage) any {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &p)
	version := defaultProtocolVersion
	if supportedVersions[p.ProtocolVersion] {
		version = p.ProtocolVersion
	}
	res := map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
		"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
	}
	if s.Instructions != "" {
		res["instructions"] = s.Instructions
	}
	return res
}

type toolResult struct {
	Content []textContent `json:"content"`
	IsError bool          `json:"isError"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func textResult(text string, isErr bool) toolResult {
	return toolResult{Content: []textContent{{Type: "text", Text: text}}, IsError: isErr}
}

func (s *Server) callTool(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Name == "" {
		return nil, &rpcError{codeInvalidParams, "tools/call needs a tool name"}
	}
	tool := s.find(p.Name)
	if tool == nil {
		return nil, &rpcError{codeInvalidParams, "unknown tool: " + p.Name}
	}

	start := s.now()
	text, err := tool.Handler(ctx, p.Arguments)
	entry := audit.Entry{Tool: tool.Name, ArgNames: argNames(p.Arguments)}
	isErr := err != nil
	if err != nil {
		text, entry.ErrorKind = describe(err)
	} else if s.MaxResultBytes > 0 && len(text) > s.MaxResultBytes {
		isErr, entry.ErrorKind = true, "result_too_large"
		text = fmt.Sprintf("The result was larger than the %d byte limit and was not returned. Ask for less data (smaller page, narrower query).", s.MaxResultBytes)
	}
	entry.IsError, entry.ResultBytes = isErr, len(text)
	entry.DurationMS = s.now().Sub(start).Milliseconds()
	s.Audit.Record(entry, start)
	return textResult(text, isErr), nil
}

func describe(err error) (message, kind string) {
	var te *ToolError
	if errors.As(err, &te) {
		return te.Message, te.Kind
	}
	// Unknown failure: say little, because the text could contain anything.
	return "The tool failed unexpectedly.", "internal"
}

func (s *Server) find(name string) *Tool {
	for i := range s.Tools {
		if s.Tools[i].Name == name {
			return &s.Tools[i]
		}
	}
	return nil
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// argNames lists the argument keys (never the values) for the audit log.
func argNames(raw json.RawMessage) []string {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func idOrNull(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

func errResp(id json.RawMessage, code int, msg string) *response {
	return &response{JSONRPC: "2.0", ID: id, Error: &rpcError{code, msg}}
}
