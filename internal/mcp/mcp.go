// Package mcp is a minimal Model Context Protocol server: the JSON-RPC 2.0 messages and the
// "tools" feature, over the stateless Streamable HTTP transport without server-sent events
// (ADR-020). It knows nothing about devices or authentication; the caller builds the Tools and
// authenticates the HTTP request before calling Handle. It uses only the standard library.
//
// Supported methods: initialize, ping, tools/list, tools/call. Notifications and client
// responses are accepted and ignored. Everything else is "method not found".
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
)

// Protocol versions this server speaks, newest first. initialize answers with the client's version
// when it is listed and otherwise with the newest, as the specification asks.
var protocolVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26"}

// JSON-RPC 2.0 error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// Server describes the server and holds its tools. Build one per request when the tools close over
// the request (the caller's identity, the audit entry); it is cheap.
type Server struct {
	Name         string
	Title        string
	Version      string
	Instructions string // shown to the model by clients that support it
	Tools        []Tool
}

// Tool is one callable. Call receives the raw `arguments` object (`{}` when the client sent none) and
// reports problems the model can fix (unknown device, bad action) as a Result with IsError, not
// as a Go error, so they reach the agent as text.
type Tool struct {
	Name        string
	Title       string
	Description string
	InputSchema map[string]any // JSON Schema of the arguments; must have "type": "object"
	Hints       Hints
	Call        func(ctx context.Context, args json.RawMessage) Result
}

// Hints are the behaviour annotations clients may use to decide whether to ask the user first.
type Hints struct {
	ReadOnly    bool // the tool changes nothing
	Destructive bool // meaningful only when not ReadOnly: it may turn something off or remove it
	Idempotent  bool // repeating the same call has no further effect
}

// Result is what a tool returns. Text is what the model reads; Data, when set, is also sent as
// structuredContent (it must marshal to a JSON object).
type Result struct {
	Text    string
	Data    any
	IsError bool
}

// Errorf is a convenience for an error Result.
func Errorf(text string) Result { return Result{Text: text, IsError: true} }

// JSON returns a successful Result whose text is the JSON encoding of data and which carries data as
// structured content. data must marshal to a JSON object.
func JSON(data any) Result {
	b, err := json.Marshal(data)
	if err != nil {
		return Result{Text: "internal error", IsError: true}
	}
	return Result{Text: string(b), Data: data}
}

// message is any JSON-RPC message: a request or notification (Method), or a response (Result/Error).
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"` // nil marshals as null
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Handle processes one HTTP request body and returns the HTTP status and the response body (nil
// when there is none to send). A request is answered 200 with a result or a JSON-RPC error; a body
// that is not a single JSON-RPC message is 400; a notification or a client response is 202.
func (s *Server) Handle(ctx context.Context, body []byte) (int, []byte) {
	body = bytes.TrimSpace(body)
	if len(body) > 0 && body[0] == '[' {
		return fail(http.StatusBadRequest, nil, codeInvalidRequest, "batches are not supported: send one message per request")
	}
	var m message
	if err := json.Unmarshal(body, &m); err != nil {
		return fail(http.StatusBadRequest, nil, codeParse, "body is not valid JSON")
	}
	if m.JSONRPC != "2.0" {
		return fail(http.StatusBadRequest, nil, codeInvalidRequest, `"jsonrpc" must be "2.0"`)
	}
	if m.Method == "" { // a client response to a server request; this server sends none
		if m.Result != nil || m.Error != nil {
			return http.StatusAccepted, nil
		}
		return fail(http.StatusBadRequest, nil, codeInvalidRequest, `"method" is required`)
	}
	if m.ID == nil { // no id: a notification, never answered
		return http.StatusAccepted, nil
	}
	if !validID(m.ID) {
		return fail(http.StatusBadRequest, nil, codeInvalidRequest, `"id" must be a string or a number`)
	}

	switch m.Method {
	case "initialize":
		return ok(m.ID, s.initialize(m.Params))
	case "ping":
		return ok(m.ID, map[string]any{})
	case "tools/list":
		return ok(m.ID, s.list())
	case "tools/call":
		res, rerr := s.call(ctx, m.Params)
		if rerr != nil {
			return fail(http.StatusOK, m.ID, rerr.Code, rerr.Message)
		}
		return ok(m.ID, res)
	}
	return fail(http.StatusOK, m.ID, codeMethodNotFound, "method not found: "+m.Method)
}

func validID(id json.RawMessage) bool {
	switch id[0] {
	case '"', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return true
	}
	return false // null, an object, an array, true, false
}

func (s *Server) initialize(params json.RawMessage) map[string]any {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &p) // a bad params object just gets the newest version
	version := protocolVersions[0]
	for _, v := range protocolVersions {
		if v == p.ProtocolVersion {
			version = v
		}
	}
	info := map[string]any{"name": s.Name, "version": s.Version}
	if s.Title != "" {
		info["title"] = s.Title
	}
	out := map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
		"serverInfo":      info,
	}
	if s.Instructions != "" {
		out["instructions"] = s.Instructions
	}
	return out
}

func (s *Server) list() map[string]any {
	tools := make([]map[string]any, 0, len(s.Tools))
	for _, t := range s.Tools {
		def := map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.InputSchema}
		if t.Title != "" {
			def["title"] = t.Title
		}
		ann := map[string]any{"readOnlyHint": t.Hints.ReadOnly, "openWorldHint": false}
		if !t.Hints.ReadOnly {
			ann["destructiveHint"] = t.Hints.Destructive
			ann["idempotentHint"] = t.Hints.Idempotent
		}
		def["annotations"] = ann
		tools = append(tools, def)
	}
	return map[string]any{"tools": tools}
}

func (s *Server) call(ctx context.Context, params json.RawMessage) (map[string]any, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Name == "" {
		return nil, &rpcError{codeInvalidParams, `"name" of the tool is required`}
	}
	var tool *Tool
	for i := range s.Tools {
		if s.Tools[i].Name == p.Name {
			tool = &s.Tools[i]
		}
	}
	if tool == nil {
		return nil, &rpcError{codeInvalidParams, "unknown tool: " + p.Name}
	}
	args := p.Arguments
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	res := tool.Call(ctx, args)
	out := map[string]any{"content": []map[string]any{{"type": "text", "text": res.Text}}}
	if res.Data != nil && !res.IsError {
		out["structuredContent"] = res.Data
	}
	if res.IsError {
		out["isError"] = true
	}
	return out, nil
}

func ok(id json.RawMessage, result any) (int, []byte) {
	b, err := json.Marshal(response{JSONRPC: "2.0", ID: id, Result: result})
	if err != nil { // cannot happen for the values built here
		return fail(http.StatusInternalServerError, id, -32603, "internal error")
	}
	return http.StatusOK, b
}

func fail(status int, id json.RawMessage, code int, msg string) (int, []byte) {
	b, _ := json.Marshal(response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
	return status, b
}
