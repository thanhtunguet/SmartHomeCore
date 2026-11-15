package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// REQ-MCP-001..012, ADR-020: the MCP server through the real router, authenticated by service
// account keys, driving the real device, command and monitoring services.

const mcpPath = "/api/v1/mcp"

type mcpReply struct {
	status int
	body   map[string]any
}

// mcpCall sends one JSON-RPC request with a bearer token.
func (e *devEnv) mcpCall(token, method string, params any) mcpReply {
	e.t.Helper()
	msg := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		msg["params"] = params
	}
	return e.mcpRaw(token, msg)
}

func (e *devEnv) mcpRaw(token string, msg any) mcpReply {
	e.t.Helper()
	rec := e.call("POST", mcpPath, msg, token)
	r := mcpReply{status: rec.Code}
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &r.body); err != nil {
			e.t.Fatalf("not JSON: %s", rec.Body)
		}
	}
	return r
}

// tool calls a tool and returns the MCP result object.
func (e *devEnv) tool(token, name string, args map[string]any) map[string]any {
	e.t.Helper()
	r := e.mcpCall(token, "tools/call", map[string]any{"name": name, "arguments": args})
	if r.status != 200 || r.body["result"] == nil {
		e.t.Fatalf("tools/call %s: %d %v", name, r.status, r.body)
	}
	return r.body["result"].(map[string]any)
}

func resultText(res map[string]any) string {
	return res["content"].([]any)[0].(map[string]any)["text"].(string)
}

func structured(t *testing.T, res map[string]any) map[string]any {
	t.Helper()
	if res["isError"] == true {
		t.Fatalf("tool failed: %s", resultText(res))
	}
	return res["structuredContent"].(map[string]any)
}

func TestMCPNeedsAServiceAccountKey(t *testing.T) {
	e := newDevEnv(t)
	key := e.mkKey("mcp", 0).Key
	ping := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}

	if r := e.mcpRaw(key, ping); r.status != 200 || r.body["result"] == nil {
		t.Fatalf("a valid key: %d %v", r.status, r.body)
	}
	want(t, e.call("POST", mcpPath, ping, ""), 401)            // anonymous
	want(t, e.call("POST", mcpPath, ping, "not.a.token"), 401) // garbage
	rec := e.callRaw("POST", mcpPath, ping, e.token)           // a valid session token, but not a key
	if rec.Code != 403 || errCode(t, rec) != "forbidden" {
		t.Fatalf("session token: %d %s", rec.Code, rec.Body)
	}
	if rec := e.callRaw("POST", mcpPath, ping, e.legacyToken(time.Hour)); rec.Code != 403 {
		t.Fatalf("legacy token: %d %s", rec.Code, rec.Body)
	}

	// Any key works whatever its scope: the key is the permission, there are no per-tool rules.
	if r := e.mcpRaw(e.mkKey("legacy", 0).Key, ping); r.status != 200 {
		t.Fatalf("a legacy-scope key: %d %v", r.status, r.body)
	}

	// A revoked key stops working at once.
	k := e.mkKey("mcp", 0)
	want(t, e.as("DELETE", "/api/v1/service-account-keys/"+k.Id.String(), nil), 204)
	want(t, e.call("POST", mcpPath, ping, k.Key), 401)

	// The transport is POST only.
	for _, m := range []string{"GET", "DELETE", "PUT"} {
		want(t, e.callRaw(m, mcpPath, nil, key), 405)
	}
}

func TestMCPHandshakeAndToolCatalogue(t *testing.T) {
	e := newDevEnv(t)
	key := e.mkKey("mcp", 0).Key

	r := e.mcpCall(key, "initialize", map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "1"},
	})
	res := r.body["result"].(map[string]any)
	if res["protocolVersion"] != "2025-06-18" || res["serverInfo"].(map[string]any)["name"] != "homecore" {
		t.Fatalf("initialize: %v", res)
	}
	// The initialized notification gets 202 and no body.
	if r := e.mcpRaw(key, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); r.status != 202 || r.body != nil {
		t.Fatalf("notification: %d %v", r.status, r.body)
	}

	r = e.mcpCall(key, "tools/list", nil)
	var names []string
	for _, tl := range r.body["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "list_devices,get_devices,control_device" {
		t.Fatalf("tools: %v", names)
	}

	// A body that is not a JSON-RPC message is a JSON-RPC error with status 400.
	rec := e.call("POST", mcpPath, `{"jsonrpc":`, key)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"code":-32700`) {
		t.Fatalf("bad json: %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("an MCP answer must not be cached: %v", rec.Header())
	}
}

func TestMCPListAndGetDevices(t *testing.T) {
	e := newDevEnv(t)
	key := e.mkKey("mcp", 0).Key
	if got := structured(t, e.tool(key, "list_devices", nil)); got["count"] != float64(0) {
		t.Fatalf("empty inventory: %v", got)
	}
	e.mkDevice(map[string]any{"name": "gaming pc", "description": "upstairs", "monitor_port": 3333, "mac_address": "AA:BB:CC:DD:EE:FF", "wol_broadcast_address": "192.168.1.255"})
	e.mkDevice(map[string]any{"name": "nas", "monitor_port": 4444})
	e.setUp(4444, true)
	e.mon.CheckDue(context.Background())

	got := structured(t, e.tool(key, "list_devices", map[string]any{}))
	list := got["devices"].([]any)
	if got["count"] != float64(2) || len(list) != 2 {
		t.Fatalf("list: %v", got)
	}
	byName := map[string]map[string]any{}
	for _, d := range list {
		m := d.(map[string]any)
		byName[m["name"].(string)] = m
	}
	pc, nas := byName["gaming pc"], byName["nas"]
	if pc["status"] != "unknown" || pc["can_turn_on"] != true || pc["can_turn_off"] != false || pc["description"] != "upstairs" || pc["power_transition"] != "none" {
		t.Fatalf("gaming pc: %v", pc)
	}
	if nas["status"] != "online" || nas["can_turn_on"] != false {
		t.Fatalf("nas: %v", nas)
	}
	for _, leaked := range []string{"mac_address", "ip_address", "agent", "AA:BB"} {
		if strings.Contains(resultText(e.tool(key, "list_devices", nil)), leaked) {
			t.Errorf("the agent view exposes %q", leaked)
		}
	}

	// get_devices: by name (any case), by id; the text is the same JSON as the structured content.
	one := e.tool(key, "get_devices", map[string]any{"device": "NAS"})
	if d := structured(t, one)["device"].(map[string]any); d["name"] != "nas" || d["status"] != "online" {
		t.Fatalf("get by name: %v", d)
	}
	id := pc["id"].(string)
	if d := structured(t, e.tool(key, "get_devices", map[string]any{"device": id}))["device"].(map[string]any); d["name"] != "gaming pc" {
		t.Fatalf("get by id: %v", d)
	}
	var fromText map[string]any
	if err := json.Unmarshal([]byte(resultText(one)), &fromText); err != nil || fromText["device"] == nil {
		t.Fatalf("text content is not the JSON result: %q", resultText(one))
	}

	// Mistakes the model can fix are tool errors, with a hint.
	for _, args := range []map[string]any{{"device": "ghost"}, {"device": "  "}, {}} {
		res := e.tool(key, "get_devices", args)
		if res["isError"] != true {
			t.Fatalf("%v: %v", args, res)
		}
	}
	if txt := resultText(e.tool(key, "get_devices", map[string]any{"device": "ghost"})); !strings.Contains(txt, "list_devices") {
		t.Errorf("not-found message gives no way forward: %q", txt)
	}
}

func TestMCPTurnOnSendsWakeOnLANOnceAndAuditsTheKey(t *testing.T) {
	e := newDevEnv(t)
	k := e.mkKey("mcp", 0)
	d := e.mkDevice(map[string]any{"name": "Gaming PC", "monitor_port": 3333, "mac_address": "AA:BB:CC:DD:EE:FF", "wol_broadcast_address": "192.168.1.255"})

	res := e.tool(k.Key, "control_device", map[string]any{"device": "gaming pc", "action": "turn_on"})
	out := structured(t, res)
	cmd := out["command"].(map[string]any)
	if out["action"] != "turn_on" || cmd["type"] != "wake" || cmd["id"] == "" || out["note"] == "" {
		t.Fatalf("turn_on: %v", out)
	}
	if got := e.packets(); len(got) != 1 || got[0] != "aa:bb:cc:dd:ee:ff 192.168.1.255:9" {
		t.Fatalf("packets: %v", got)
	}
	// The command is a normal command, visible through the REST API.
	if rec := e.as("GET", "/api/v1/commands/"+cmd["id"].(string), nil); rec.Code != 200 {
		t.Fatalf("command %v: %d", cmd["id"], rec.Code)
	}

	// Audited as the key's owner, naming the key and the door.
	rows := e.auditRows("device.wake")
	if len(rows) != 1 || rows[0].actor != "alice" || rows[0].target != d.Id.String() ||
		!strings.Contains(rows[0].details, `"via":"mcp"`) || !strings.Contains(rows[0].details, k.Id.String()) {
		t.Fatalf("audit: %+v", rows)
	}

	// Idempotent after the throttle window: the active command is reused, no second packet.
	settle()
	again := structured(t, e.tool(k.Key, "control_device", map[string]any{"device": d.Id.String(), "action": "turn_on"}))
	if again["command"].(map[string]any)["id"] != cmd["id"] || len(e.packets()) != 1 {
		t.Fatalf("not idempotent: %v packets=%v", again, e.packets())
	}
}

// The MCP door shares the one-request-per-second slot with REST and the legacy endpoints.
func TestMCPPowerRequestsAreThrottledPerDevice(t *testing.T) {
	e := newDevEnv(t)
	key := e.mkKey("mcp", 0).Key
	d := e.mkDevice(map[string]any{"name": "tv", "monitor_port": 5555, "mac_address": "AA:BB:CC:DD:EE:01", "wol_broadcast_address": "192.168.1.255"})

	structured(t, e.tool(key, "control_device", map[string]any{"device": "tv", "action": "turn_on"}))
	res := e.tool(key, "control_device", map[string]any{"device": "tv", "action": "turn_on"})
	if res["isError"] != true || !strings.Contains(resultText(res), "wait") {
		t.Fatalf("second request in the same second: %v", res)
	}
	want(t, e.as("POST", "/api/v1/devices/"+d.Id.String()+"/wake", nil), 429) // same slot as REST
	if len(e.packets()) != 1 {
		t.Fatalf("throttled requests sent packets: %v", e.packets())
	}
	// Reading is never throttled.
	structured(t, e.tool(key, "get_devices", map[string]any{"device": "tv"}))
	structured(t, e.tool(key, "control_device", map[string]any{"device": "tv", "action": "check_status"}))
}

func TestMCPControlDeviceErrors(t *testing.T) {
	e := newDevEnv(t)
	key := e.mkKey("mcp", 0).Key
	e.mkDevice(map[string]any{"name": "no-mac", "monitor_port": 1})

	for name, c := range map[string]struct {
		args map[string]any
		in   string // what the message must say
	}{
		"unknown device":   {map[string]any{"device": "ghost", "action": "turn_on"}, "list_devices"},
		"unknown action":   {map[string]any{"device": "no-mac", "action": "reboot"}, "turn_on"},
		"no action":        {map[string]any{"device": "no-mac"}, "action"},
		"wake without MAC": {map[string]any{"device": "no-mac", "action": "turn_on"}, ""},
		"no agent":         {map[string]any{"device": "no-mac", "action": "turn_off"}, ""},
	} {
		if c.args["action"] == "turn_on" || c.args["action"] == "turn_off" {
			settle() // both reach the per-device throttle on the same device
		}
		res := e.tool(key, "control_device", c.args)
		if res["isError"] != true || !strings.Contains(resultText(res), c.in) || resultText(res) == "" {
			t.Errorf("%s: %v", name, res)
		}
	}
	if len(e.packets()) != 0 {
		t.Fatalf("a failed request sent packets: %v", e.packets())
	}

	// A bad tool name or bad arguments type is a protocol error / tool error, never a 5xx.
	r := e.mcpCall(key, "tools/call", map[string]any{"name": "wipe_disk"})
	if r.status != 200 || r.body["error"].(map[string]any)["code"] != float64(-32602) {
		t.Fatalf("unknown tool: %d %v", r.status, r.body)
	}
	r = e.mcpCall(key, "tools/call", map[string]any{"name": "control_device", "arguments": []any{1}})
	if r.status != 200 || r.body["result"].(map[string]any)["isError"] != true {
		t.Fatalf("array arguments: %d %v", r.status, r.body)
	}
}

func TestMCPCheckStatusProbesNow(t *testing.T) {
	e := newDevEnv(t)
	key := e.mkKey("mcp", 0).Key
	e.mkDevice(map[string]any{"name": "nas", "monitor_port": 4444})
	check := func() map[string]any {
		return structured(t, e.tool(key, "control_device", map[string]any{"device": "nas", "action": "check_status"}))
	}

	if got := check(); got["reachable"] != false || got["device"].(map[string]any)["last_checked_at"] == nil {
		t.Fatalf("down: %v", got)
	}
	e.setUp(4444, true)
	// The probe is made now, without waiting for the monitor's next tick.
	got := check()
	if got["reachable"] != true || got["device"].(map[string]any)["last_seen_at"] == nil {
		t.Fatalf("up: %v", got)
	}
}

// A shutdown through MCP reaches the real agent, like REST and the legacy endpoints.
func TestMCPTurnOffReachesTheAgent(t *testing.T) {
	se := newShutdownEnv(t, nil)
	k := se.mkKey("mcp", 0)

	out := structured(t, se.tool(k.Key, "control_device", map[string]any{"device": "GAMING PC", "action": "turn_off"}))
	if out["command"].(map[string]any)["type"] != "shutdown" {
		t.Fatalf("turn_off: %v", out)
	}
	se.waitPower(1)
	rows := se.auditRows("device.shutdown")
	if len(rows) != 1 || rows[0].actor != "alice" || !strings.Contains(rows[0].details, `"via":"mcp"`) || !strings.Contains(rows[0].details, k.Id.String()) {
		t.Fatalf("audit: %+v", rows)
	}

	// The machine goes down; check_status sees it. (The probe is the real TCP one here.)
	se.mach.powerOff()
	var last map[string]any
	for range 3 { // offline_threshold is 2 misses
		last = structured(t, se.tool(k.Key, "control_device", map[string]any{"device": "gaming pc", "action": "check_status"}))
	}
	if d := last["device"].(map[string]any); d["status"] != "offline" || last["reachable"] != false {
		t.Fatalf("after power-off: %v", last)
	}
}

// Waking a machine that is already online is a command born completed (ADR-012): no packet, and the
// note says so instead of promising a change.
func TestMCPTurnOnOfAnOnlineDeviceDoesNothing(t *testing.T) {
	e := newDevEnv(t)
	key := e.mkKey("mcp", 0).Key
	e.mkDevice(map[string]any{"name": "nas", "monitor_port": 4444, "mac_address": "AA:BB:CC:DD:EE:02", "wol_broadcast_address": "192.168.1.255"})
	e.setUp(4444, true)
	e.mon.CheckDue(context.Background())

	out := structured(t, e.tool(key, "control_device", map[string]any{"device": "nas", "action": "turn_on"}))
	if out["command"].(map[string]any)["status"] != "completed" || !strings.Contains(out["note"].(string), "already online") {
		t.Fatalf("turn_on of an online device: %v", out)
	}
	if len(e.packets()) != 0 {
		t.Fatalf("packets sent for an online device: %v", e.packets())
	}
}
