package tools

import (
	"context"

	"github.com/didenkolab/dbgmcp/internal/discover"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ListTargetsIn struct {
	WorkDir string `json:"work_dir" jsonschema:"Absolute path of the project root to scan."`
}

type ListTargetsOut struct {
	Targets []discover.Target `json:"targets"`
	Message string            `json:"message"`
}

// listDebugTargets replaces the IDE's list of run configurations. There is no
// IDE here, so nothing was configured by a human in advance and the targets
// have to come from the project itself.
func (r *Registry) listDebugTargets(ctx context.Context, _ *mcp.CallToolRequest, in ListTargetsIn) (*mcp.CallToolResult, ListTargetsOut, error) {
	if in.WorkDir == "" {
		return fail[ListTargetsOut]("Missing required parameter: work_dir")
	}
	targets, err := discover.Go(ctx, in.WorkDir)
	if err != nil {
		return fail[ListTargetsOut]("%s", err.Error())
	}
	out := ListTargetsOut{Targets: targets}
	if len(targets) == 0 {
		out.Message = "No main packages and no tests were found under " + in.WorkDir + "."
	} else {
		out.Message = "Pass a target's import_path as 'target' to start_debug_session, with its 'mode'. " +
			"For a test target, set test_run to one of the listed test names to run just that one."
	}
	return ok(out)
}
