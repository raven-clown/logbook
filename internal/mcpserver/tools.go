package mcpserver

import (
	"context"
	"errors"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/raven-clown/logbook/internal/wire"
	"github.com/raven-clown/logbook/memory"
)

func asMemoryError(err error, target **memory.Error) bool {
	return errors.As(err, target)
}

func rememberTool() mcp.Tool {
	return mcp.NewTool("remember",
		mcp.WithDescription("Store something worth knowing later, for you and for other agents and people. Use corrects to fix a memory that turned out to be wrong."),
		mcp.WithInputSchema[wire.Remember](),
		mcp.WithOutputSchema[memory.RememberResult]())
}

func (h *handlers) remember(ctx context.Context, a wire.Remember) (any, error) {
	r, err := a.ToRequest()
	if err != nil {
		return nil, err
	}
	return h.svc.Remember(ctx, h.principal(ctx), r)
}

func recallTool() mcp.Tool {
	return mcp.NewTool("recall",
		mcp.WithDescription("Search shared memories before answering a question that may have been answered before. Results say whether each memory was verified, disputed, corrected or is stale."),
		mcp.WithInputSchema[wire.Recall](),
		mcp.WithOutputSchema[memory.RecallResult]())
}

func (h *handlers) recall(ctx context.Context, a wire.Recall) (any, error) {
	r, err := a.ToRequest()
	if err != nil {
		return nil, err
	}
	return h.svc.Recall(ctx, h.principal(ctx), r)
}

func verifyTool() mcp.Tool {
	return mcp.NewTool("verify",
		mcp.WithDescription("Confirm or dispute a memory after checking it. Confirming your own memory from the same channel is recorded but does not count."),
		mcp.WithInputSchema[wire.Verify](),
		mcp.WithOutputSchema[memory.VerifyResult]())
}

func (h *handlers) verify(ctx context.Context, a wire.Verify) (any, error) {
	return h.svc.Verify(ctx, h.principal(ctx), a.ToRequest())
}

func forgetTool() mcp.Tool {
	return mcp.NewTool("forget",
		mcp.WithDescription("Hide a memory that should not be used any more. Prefer remember with corrects when the memory is wrong rather than unwanted."),
		mcp.WithInputSchema[wire.Forget](),
		mcp.WithOutputSchema[memory.ForgetResult]())
}

func (h *handlers) forget(ctx context.Context, a wire.Forget) (any, error) {
	req := a.ToRequest()
	m, err := h.svc.Forget(ctx, h.principal(ctx), req)
	if err != nil {
		return nil, err
	}
	return memory.NewForgetResult(m), nil
}

func listTool() mcp.Tool {
	return mcp.NewTool("list_memories",
		mcp.WithDescription("Browse memories by scope, status, kind or tag. order review gives the queue of memories that need someone to check them."),
		mcp.WithInputSchema[wire.List](),
		mcp.WithOutputSchema[memory.ListResult]())
}

func (h *handlers) list(ctx context.Context, a wire.List) (any, error) {
	return h.svc.List(ctx, h.principal(ctx), a.ToRequest())
}
