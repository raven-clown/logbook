// Package mcpserver serves logbook's memory tools over MCP.
package mcpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/raven-clown/logbook/memory"
)

const instructions = "logbook is a memory shared with other agents and people. " +
	"Call recall before answering a question that may have been answered before, " +
	"remember when you learn something others should know, " +
	"and remember with corrects when you find that a memory is wrong."

// New returns an MCP server with the five memory tools. principal says who is
// calling, from the request context.
func New(svc *memory.Service, principal func(ctx context.Context) memory.Principal, version string) *server.MCPServer {
	s := server.NewMCPServer("logbook", version, server.WithToolCapabilities(false), server.WithInstructions(instructions), server.WithRecovery())
	h := &handlers{svc: svc, principal: principal}
	s.AddTool(rememberTool(), bind(h.remember))
	s.AddTool(recallTool(), bind(h.recall))
	s.AddTool(verifyTool(), bind(h.verify))
	s.AddTool(forgetTool(), bind(h.forget))
	s.AddTool(listTool(), bind(h.list))
	return s
}

type handlers struct {
	svc       *memory.Service
	principal func(ctx context.Context) memory.Principal
}

func bind[T any](fn func(ctx context.Context, args T) (any, error)) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args T
		if err := req.BindArguments(&args); err != nil {
			return errorResult(memory.Errorf(memory.CodeInvalidArgument, "", "arguments do not match the tool's schema: %v", err)), nil
		}
		out, err := fn(ctx, args)
		if err != nil {
			return errorResult(err), nil
		}
		return mcp.NewToolResultStructuredOnly(out), nil
	}
}

type errorBody struct {
	Error struct {
		Code    memory.Code `json:"code"`
		Message string      `json:"message"`
		Field   string      `json:"field,omitempty"`
	} `json:"error"`
}

func errorResult(err error) *mcp.CallToolResult {
	var body errorBody
	body.Error.Code = memory.CodeOf(err)
	body.Error.Message = err.Error()
	var e *memory.Error
	if asMemoryError(err, &e) {
		body.Error.Field = e.Field
	}
	if body.Error.Code == memory.CodeInternal {
		slog.Error("tool call failed", "err", err)
		body.Error.Message = "internal error"
	}
	b, _ := json.Marshal(body)
	return mcp.NewToolResultError(string(b))
}

func parseTime(field, s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, memory.Errorf(memory.CodeInvalidArgument, field, "%s must be an RFC 3339 time such as 2026-11-04T09:00:00Z", field)
	}
	return t, nil
}
