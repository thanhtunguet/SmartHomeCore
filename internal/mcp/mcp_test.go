package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// REQ-MCP-001..004: the protocol layer, independent of devices and HTTP.

func testServer() *Server {
	return &Server{
		Name: "t", Version: "1", Instructions: "hi",
		Tools: []Tool{
			{
				Name: "echo", Description: "echo", InputSchema: map[string]any{"type": "object"},
				Hints: Hints{ReadOnly: true},
				Call: func(_ context.Context, args json.RawMessage) Result {
					return JSON(map[string]any{"args": json.RawMessage(args)})
				},
			},
			{
				Name: "boom", Description: "fails", InputSchema: map[string]any{"type": "object"},
				Call: func(context.Context, json.RawMessage) Result { return Errorf("device not found") },
			},
		},
	}
}

func rpc(t *testing.T, s *Server, body string) (int, map[string]any) {
	t.Helper()
	status, out := s.Handle(context.Background(), []byte(body))
	if out == nil {
		return status, nil
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("response is not JSON: %q", out)
	}
	if m["jsonrpc"] != "2.0" {
		t.Fatalf("response lacks jsonrpc 2.0: %s", out)
	}
	return status, m
}

func errCode(t *testing.T, m map[string]any) float64 {
	t.Helper()
	e, ok := m["error"].(map[string]any)
	if !ok {
		t.Fatalf("not an error response: %v", m)
	}
	return e["code"].(float64)
}

func TestInitializeNegotiatesTheProtocolVersion(t *testing.T) {
	s := testServer()
	for _, c := range []struct{ asked, want string }{
		{"2025-06-18", "2025-06-18"},
		{"2025-03-26", "2025-03-26"},
		{"2025-11-25", "2025-11-25"},
		{"1999-01-01", protocolVersions[0]}, // unknown: the newest we speak
		{"", protocolVersions[0]},
	} {
		status, m := rpc(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+c.asked+`","capabilities":{},"clientInfo":{"name":"c","version":"1"}}}`)
		if status != 200 {
			t.Fatalf("status %d", status)
		}
		res := m["result"].(map[string]any)
		if res["protocolVersion"] != c.want {
			t.Errorf("asked %q: got %v, want %s", c.asked, res["protocolVersion"], c.want)
		}
		if _, ok := res["capabilities"].(map[string]any)["tools"]; !ok {
			t.Errorf("tools capability missing: %v", res)
		}
		if res["serverInfo"].(map[string]any)["name"] != "t" || res["instructions"] != "hi" {
			t.Errorf("serverInfo/instructions: %v", res)
		}
	}
}

func TestIDIsEchoedExactly(t *testing.T) {
	s := testServer()
	for _, id := range []string{`7`, `"abc"`, `0`, `-3`} {
		_, out := s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":`+id+`,"method":"ping"}`))
		if !strings.Contains(string(out), `"id":`+id+`,`) && !strings.Contains(string(out), `"id":`+id+`}`) {
			t.Errorf("id %s not echoed: %s", id, out)
		}
	}
	if status, m := rpc(t, s, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); status != 200 || m["result"] == nil {
		t.Fatalf("ping: %d %v", status, m)
	}
}

func TestToolsListAndCall(t *testing.T) {
	s := testServer()
	_, m := rpc(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools := m["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools: %v", tools)
	}
	echo := tools[0].(map[string]any)
	ann := echo["annotations"].(map[string]any)
	if echo["name"] != "echo" || echo["inputSchema"].(map[string]any)["type"] != "object" || ann["readOnlyHint"] != true {
		t.Fatalf("echo definition: %v", echo)
	}
	if _, has := ann["destructiveHint"]; has {
		t.Errorf("a read-only tool must not carry destructiveHint: %v", ann)
	}

	// Arguments reach the tool; the result has text and structured content.
	_, m = rpc(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"a":1}}}`)
	res := m["result"].(map[string]any)
	if res["isError"] != nil {
		t.Fatalf("unexpected isError: %v", res)
	}
	text := res["content"].([]any)[0].(map[string]any)
	if text["type"] != "text" || !strings.Contains(text["text"].(string), `"a":1`) {
		t.Fatalf("content: %v", res["content"])
	}
	if res["structuredContent"].(map[string]any)["args"].(map[string]any)["a"] != float64(1) {
		t.Fatalf("structuredContent: %v", res["structuredContent"])
	}

	// No arguments at all is `{}`, not a failure.
	for _, call := range []string{`{"name":"echo"}`, `{"name":"echo","arguments":null}`} {
		_, m = rpc(t, s, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":`+call+`}`)
		if m["result"] == nil {
			t.Fatalf("%s: %v", call, m)
		}
	}

	// A tool's own failure is a result the model can read, not a protocol error.
	_, m = rpc(t, s, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"boom"}}`)
	res = m["result"].(map[string]any)
	if res["isError"] != true || res["content"].([]any)[0].(map[string]any)["text"] != "device not found" {
		t.Fatalf("tool error: %v", m)
	}
	if _, has := res["structuredContent"]; has {
		t.Errorf("an error result carries no structured content: %v", res)
	}
}

func TestProtocolErrors(t *testing.T) {
	s := testServer()
	for _, c := range []struct {
		name, body string
		status     int
		code       float64
	}{
		{"unknown method", `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`, 200, codeMethodNotFound},
		{"unknown tool", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nope"}}`, 200, codeInvalidParams},
		{"missing tool name", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`, 200, codeInvalidParams},
		{"no params", `{"jsonrpc":"2.0","id":1,"method":"tools/call"}`, 200, codeInvalidParams},
		{"not json", `{"jsonrpc":`, 400, codeParse},
		{"empty body", ``, 400, codeParse},
		{"batch", `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, 400, codeInvalidRequest},
		{"wrong version", `{"jsonrpc":"1.0","id":1,"method":"ping"}`, 400, codeInvalidRequest},
		{"null id", `{"jsonrpc":"2.0","id":null,"method":"ping"}`, 400, codeInvalidRequest},
		{"object id", `{"jsonrpc":"2.0","id":{},"method":"ping"}`, 400, codeInvalidRequest},
		{"no method", `{"jsonrpc":"2.0","id":1}`, 400, codeInvalidRequest},
	} {
		t.Run(c.name, func(t *testing.T) {
			status, m := rpc(t, s, c.body)
			if status != c.status || errCode(t, m) != c.code {
				t.Fatalf("status %d code %v, want %d %v: %v", status, m["error"], c.status, c.code, m)
			}
		})
	}
}

// Notifications and client responses are accepted without a body (202).
func TestNotificationsAndResponsesAreAcknowledgedSilently(t *testing.T) {
	s := testServer()
	for _, body := range []string{
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`,
		`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"boom"}}`, // no id: never executed as a call
		`{"jsonrpc":"2.0","id":9,"result":{}}`,
		`{"jsonrpc":"2.0","id":9,"error":{"code":-1,"message":"x"}}`,
	} {
		status, out := s.Handle(context.Background(), []byte(body))
		if status != http.StatusAccepted || out != nil {
			t.Errorf("%s → %d %q, want 202 with no body", body, status, out)
		}
	}
}
