package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/adro-project/adro/internal/domain"
	mcpclient "github.com/adro-project/adro/internal/mcp"
)

// MCPToolExecutor binds the protocol-only MCP client to the durable ToolLoop.
// The client never decides approval, retries, receipts, or unknown-outcome
// semantics; those remain in the runtime journal and frozen ToolContract.
type MCPToolExecutor struct {
	Client mcpclient.Client
	Server domain.MCPServer
	Loop   ToolLoop
}

func (e MCPToolExecutor) Run(ctx context.Context, callID string, contract ToolContract, input map[string]any) (ToolExecution, error) {
	if e.Loop.Journal == nil {
		return ToolExecution{}, errors.New("MCP ToolLoop journal is required")
	}
	if e.Server.ID == "" && e.Server.Endpoint == "" {
		return ToolExecution{}, errors.New("MCP server is required")
	}
	if contract.Name == "" {
		return ToolExecution{}, errors.New("MCP tool contract name is required")
	}
	return e.Loop.Run(ctx, callID, contract, input, func(execCtx context.Context) (any, error) {
		result, err := e.Client.Invoke(execCtx, e.Server, contract.Name, input)
		if err != nil {
			return nil, fmt.Errorf("MCP tool %q: %w", contract.Name, err)
		}
		return result, nil
	})
}
