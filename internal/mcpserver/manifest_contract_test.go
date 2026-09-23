package mcpserver_test

import (
	"context"
	"sort"
	"strings"
	"testing"
)

// This file is the contract with MCP clients: which tools exist, and which of
// their names are promised to mean the same thing in the JetBrains debugger
// plugin.
//
// It plays the role ToolManifestContractTest plays there. Without it, adding or
// renaming a tool here is silent -- which is how it was found: diff_runs was
// registered and the whole suite stayed green.

// advertisedTools is every tool this server offers, held separately from
// register.go on purpose. A list derived from the registry would agree with the
// registry by construction and could never disagree with it, so a tool removed
// from both in one commit would still pass.
var advertisedTools = []string{
	"describe_backend",
	"diff_runs",
	"evaluate_expression",
	"explain_value",
	"get_debug_session_status",
	"get_session_output",
	"get_source_context",
	"get_stack_trace",
	"get_unit_ancestors",
	"get_variables",
	"list_breakpoints",
	"list_debug_sessions",
	"list_debug_targets",
	"list_execution_units",
	"pause_execution",
	"remove_breakpoint",
	"resume_execution",
	"run_to_line",
	"select_stack_frame",
	"set_breakpoint",
	"set_variable",
	"set_watchpoint",
	"start_debug_session",
	"step_into",
	"step_out",
	"step_over",
	"stop_debug_session",
	"trace_execution",
	"wait_for_pause",
}

// sharedWithTheJetBrainsPlugin are the names that must keep meaning the same
// thing in both servers, because one agent skill drives an IDE session and a
// headless one without branching on which it got.
//
// Renaming one of these here is a breaking change for that skill even though
// nothing in this repository would otherwise notice.
var sharedWithTheJetBrainsPlugin = []string{
	"describe_backend",
	"evaluate_expression",
	"get_debug_session_status",
	"get_session_output",
	"get_source_context",
	"get_stack_trace",
	"get_variables",
	"list_breakpoints",
	"list_debug_sessions",
	"list_execution_units",
	"pause_execution",
	"remove_breakpoint",
	"resume_execution",
	"run_to_line",
	"select_stack_frame",
	"set_breakpoint",
	"set_variable",
	"start_debug_session",
	"step_into",
	"step_out",
	"step_over",
	"stop_debug_session",
	"trace_execution",
	"wait_for_pause",
}

func registeredToolNames(t *testing.T) map[string]bool {
	t.Helper()
	res, err := connect(t).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	return names
}

func TestEveryAdvertisedToolIsRegisteredAndNoOthers(t *testing.T) {
	registered := registeredToolNames(t)

	var missing []string
	for _, name := range advertisedTools {
		if !registered[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("advertised here but not registered by Register(): %s", strings.Join(missing, ", "))
	}

	advertised := map[string]bool{}
	for _, name := range advertisedTools {
		advertised[name] = true
	}
	var unlisted []string
	for name := range registered {
		if !advertised[name] {
			unlisted = append(unlisted, name)
		}
	}
	sort.Strings(unlisted)
	if len(unlisted) > 0 {
		t.Errorf("registered but not advertised here -- add them to advertisedTools, to README.md, to docs/USING.md and to skill/SKILL.md: %s",
			strings.Join(unlisted, ", "))
	}
}

func TestTheNamesSharedWithTheJetBrainsPluginAreStillOffered(t *testing.T) {
	registered := registeredToolNames(t)
	for _, name := range sharedWithTheJetBrainsPlugin {
		if !registered[name] {
			t.Errorf("%s is part of the vocabulary shared with the JetBrains plugin and is no longer offered here; "+
				"renaming it breaks any skill written against both", name)
		}
	}
}

func TestTheAdvertisedListIsSortedAndFreeOfDuplicates(t *testing.T) {
	// Not cosmetic: an unsorted list is where a duplicate hides, and a duplicate
	// makes the count assertions above agree with themselves while being wrong.
	seen := map[string]bool{}
	for i, name := range advertisedTools {
		if seen[name] {
			t.Errorf("%s is listed twice", name)
		}
		seen[name] = true
		if i > 0 && advertisedTools[i-1] > name {
			t.Errorf("%s is out of order, after %s", name, advertisedTools[i-1])
		}
	}
}
