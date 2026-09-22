# dbgmcp

Debug Go programs with breakpoints, from an AI agent, **without an IDE**.

`dbgmcp` is an MCP server that drives a headless [Delve](https://github.com/go-delve/delve) over
its native RPC API. It runs anywhere Go runs -- a terminal, a container, CI -- and needs no editor.

```
agent --MCP stdio--> dbgmcp --rpc2 over unix socket--> dlv --headless --> your program
```

## Install

```bash
go install github.com/didenkolab/dbgmcp/cmd/dbgmcp@latest
dbgmcp doctor        # checks that Delve is present and reachable
```

Delve is required and pinned to v1.27.2:

```bash
go install github.com/go-delve/delve/cmd/dlv@v1.27.2
```

Register it with your agent as an MCP server over stdio, command `dbgmcp`.

## Use

```
start_debug_session -> set_breakpoint -> resume_execution -> wait_for_pause -> evaluate_expression
```

`start_debug_session` leaves the target stopped before its first instruction, so set every
breakpoint before the first resume. `wait_for_pause` returns the stack, the variables and the
surrounding source in one response -- there is usually no need to follow it with `get_variables`
or `get_stack_trace`.

## What it does differently

**Break by symbol.** `set_breakpoint {"symbol": "main.lineTotal"}`. No reading the file, no
counting lines, no breakage when something is inserted above.

**One response carries the whole situation.** `wait_for_pause` returns location, reason,
execution unit, stack, variables and source together. The benchmark for this project is the
acceptance test, which locates a wrong value at runtime in **six MCP calls** and fails if that
number grows.

**Variables come back flat, and the path is the expression.**

```json
{"name": "it",       "type": "main.Item", "value": "main.Item{Name: \"chair\", Price: 150, Qty: 4}"}
{"name": "it.Price", "type": "int",       "value": "150"}
```

`it.Price` is both the answer and the next call's input. A nested tree would cost more tokens and
be less useful.

**Capabilities are data.** `describe_backend` reports what this debugger can actually do --
watchpoints, non-suspending tracing, per-unit hit counts, calling functions during evaluation.
Plan against it instead of discovering the limits by failing into them.

**`trace_execution`: a whole run in one call.** Declare where to record and what to record, and get
the transcript back from one call. Delve evaluates the expressions itself at every hit and resumes
on its own, so watching a thousand iterations costs one call, not a thousand.

The saving is **round trips, not observer effect**, and the result says which you got. Without eBPF
-- so on macOS always, and on Linux unless `dlv --ebpf` is enabled and privileged -- Delve stops the
debuggee at every hit and resumes it itself, so the transcript reports `mode: "auto_continue"` and
`perturbs_timing: true`. Genuinely non-stop tracing is `mode: "buffered"`, and it is not available
here. An agent chasing a race reads that from the response rather than from this paragraph.

**It finds its own targets.** `list_debug_targets` reads the project and reports the main packages
and every test function by name. An IDE plugin can list run configurations because a human made
them; with no IDE there is nothing to list, so they are derived from the source instead -- without
executing anything.

**Tool names match the [JetBrains debugger MCP plugin](https://github.com/hechtcarmel/jetbrains-debugger-mcp-plugin)**
wherever the semantics match, so one agent and one skill work with an IDE and without one.

## Known gaps

Stated plainly, so nobody mistakes the test suite for more than it is.

- **Go only.** The backend interface and the capability model are built to take a second backend
  (DAP for Python, Node, Rust; CDP for browsers), but none exists yet, and an abstraction with one
  implementation is usually wrong. Treat the interface as unproven.
- **Launch only.** `test`, `debug` and `exec`. There is no attaching to a process this server did
  not start, and no remote or containerised target. `PathMapping` is modelled but not exercised.
- **No `findings`.** The design's analytics layer -- anomalies computed from a transcript, such
  as a value that was monotonic and stopped being -- does not exist. `trace_execution` returns the
  transcript; reading it is still the agent's job.
- **No safety guard on evaluation.** Delve does not call functions by default, which the
  conformance suite verifies, so the worst of the risk is absent rather than defended against.
- **Watchpoints are hardware watchpoints.** At most four exist at once, and each is bound to the
  stack frame it was set in, so it vanishes when that frame returns. The variable must already be
  in scope: stopping at a function's entry is before its locals are declared.
- **Goroutine ancestry costs performance** and is therefore off unless `record_ancestry` is set on
  the session.
- **Tested on darwin/arm64 only.** linux/amd64 is expected to work and is not yet verified.

## Keeping the capability claims honest

`describe_backend` is only worth reading if it is true, so every field of it is exercised by a
single suite in `internal/backend/conformance`, run against each backend. A declared capability
must demonstrably work; an undeclared one must refuse with a message naming what is missing.

This is not decoration. The first run of that suite caught a capability this server was declaring
and could not deliver, in the first commit that declared it.

## Development

```bash
go test ./...        # includes live tests that launch a real dlv against testdata/buggy
dbgmcp doctor
```

The live tests skip when Delve is absent rather than failing. There are no mocked debuggers in
this repository on purpose: a mocked debugger proves nothing about whether this can debug.
