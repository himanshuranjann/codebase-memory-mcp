package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/GoHighLevel/codebase-memory-mcp/ghl/internal/bridge"
	"github.com/GoHighLevel/codebase-memory-mcp/ghl/internal/mcp"
)

type fakeBridgeClient struct {
	info       mcp.ServerInfo
	callMethod string
	callParams interface{}
	callResult json.RawMessage
	callErr    error
	toolName   string
	toolArgs   map[string]interface{}
	toolResult *mcp.ToolResult
	toolErr    error
}

func (f *fakeBridgeClient) ServerInfo() mcp.ServerInfo {
	return f.info
}

func (f *fakeBridgeClient) Call(_ context.Context, method string, params interface{}) (json.RawMessage, error) {
	f.callMethod = method
	f.callParams = params
	return f.callResult, f.callErr
}

func (f *fakeBridgeClient) CallTool(_ context.Context, name string, params map[string]interface{}) (*mcp.ToolResult, error) {
	f.toolName = name
	f.toolArgs = params
	return f.toolResult, f.toolErr
}

func TestMCPBridgeBackendInitializeNegotiatesProtocol(t *testing.T) {
	backend := &mcpBridgeBackend{
		client: &fakeBridgeClient{
			info: mcp.ServerInfo{Name: "codebase-memory-mcp", Version: "0.10.0"},
		},
	}

	raw, err := backend.Call("initialize", json.RawMessage(`{"protocolVersion":"2025-03-26"}`))
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}

	var result struct {
		ProtocolVersion string                 `json:"protocolVersion"`
		Capabilities    map[string]interface{} `json:"capabilities"`
		ServerInfo      mcp.ServerInfo         `json:"serverInfo"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("parse initialize result: %v", err)
	}

	if result.ProtocolVersion != "2025-03-26" {
		t.Errorf("protocolVersion: want 2025-03-26, got %q", result.ProtocolVersion)
	}
	if result.ServerInfo.Version != "0.10.0" {
		t.Errorf("server version: want 0.10.0, got %q", result.ServerInfo.Version)
	}
	if _, ok := result.Capabilities["tools"]; !ok {
		t.Errorf("capabilities.tools: expected tools capability")
	}
}

func TestMCPBridgeBackendForwardsToolsList(t *testing.T) {
	client := &fakeBridgeClient{
		callResult: json.RawMessage(`{"tools":[{"name":"list_projects"}]}`),
	}
	backend := &mcpBridgeBackend{client: client}

	raw, err := backend.Call("tools/list", nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}

	if client.callMethod != "tools/list" {
		t.Errorf("call method: want tools/list, got %q", client.callMethod)
	}
	if string(raw) != `{"tools":[{"name":"list_projects"}]}` {
		t.Errorf("raw result: got %s", raw)
	}
}

func TestMCPBridgeBackendForwardsToolsCall(t *testing.T) {
	client := &fakeBridgeClient{
		toolResult: &mcp.ToolResult{
			Content: []mcp.Content{{Type: "text", Text: "ok"}},
		},
	}
	backend := &mcpBridgeBackend{client: client}

	raw, err := backend.Call("tools/call", json.RawMessage(`{"name":"list_projects","arguments":{"project":"demo"}}`))
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}

	if client.toolName != "list_projects" {
		t.Errorf("tool name: want list_projects, got %q", client.toolName)
	}
	if got := client.toolArgs["project"]; got != "demo" {
		t.Errorf("tool args.project: want demo, got %v", got)
	}
	if string(raw) != `{"content":[{"type":"text","text":"ok"}],"isError":false}` {
		t.Errorf("raw result: got %s", raw)
	}
}

func TestMCPBridgeBackendRejectsUnknownMethod(t *testing.T) {
	backend := &mcpBridgeBackend{client: &fakeBridgeClient{}}

	_, err := backend.Call("resources/list", nil)
	if err == nil {
		t.Fatal("expected error for unknown method")
	}
	if err != bridge.ErrMethodNotFound {
		t.Fatalf("want ErrMethodNotFound, got %v", err)
	}
}
