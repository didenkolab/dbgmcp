# dbgmcp

[![test](https://github.com/didenkolab/dbgmcp/actions/workflows/test.yml/badge.svg)](https://github.com/didenkolab/dbgmcp/actions/workflows/test.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/didenkolab/dbgmcp.svg)](https://pkg.go.dev/github.com/didenkolab/dbgmcp)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Debug programs with breakpoints, from an AI agent, **without an IDE**. Go through Delve's native API; Python through DAP.

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

Each value also carries `presence`, which separates a real value from a `nil` and from one the
debugger could not read. All three used to render as the same string, which produced two defects and
would have produced false divergences the moment two runs were compared.

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

**It reads the transcript for you.** `trace_execution` comes back with `findings`: a value that had
been climbing and reversed, one that had always been present and arrived empty, one that changed
every iteration and quietly stopped. Each carries the values it rests on, and each is an
observation rather than a verdict -- the reader draws the conclusion.

These notice an anomalous *shape*, not a wrong *value*. No rule can tell a wrong number from a
right one without knowing the expected answer; comparing two runs is what does that, and it is not
built yet.

**It can see what the program printed.** `get_session_output` returns the debuggee's stdout and
stderr, with a cursor for tailing. Outside an IDE there is no console, so without this a panic
message -- often the shortest path to the answer -- would be invisible.

**It finds its own targets.** `list_debug_targets` reads the project and reports the main packages
and every test function by name. An IDE plugin can list run configurations because a human made
them; with no IDE there is nothing to list, so they are derived from the source instead -- without
executing anything.

**Tool names match the [JetBrains debugger MCP plugin](https://github.com/hechtcarmel/jetbrains-debugger-mcp-plugin)**
wherever the semantics match, so one agent and one skill work with an IDE and without one.

## Known gaps

Stated plainly, so nobody mistakes the test suite for more than it is.

- **Go and Python.** Go goes through Delve's native API, Python through DAP. JavaScript and Ruby
  are the same DAP backend plus an adapter profile, and are not written yet.
- **Python has a smaller capability set, and says so.** No watchpoints, no breakpoints by symbol
  (debugpy binds them but reports no location, so an agent cannot know where it will stop), no
  hit counts, no ancestry. `describe_backend` reports each of these, and the conformance suite
  checks the refusals as well as the support.
- **Local only.** `test`, `debug`, `exec` and `attach` all work, but only against processes on this
  machine. No remote or containerised target; `PathMapping` is modelled and not exercised.
- **Attaching on Linux is limited by Yama.** `ptrace_scope` is 1 on most
  distributions and on GitHub's runners, which allows a process to debug only its own
  descendants -- so attaching to a service started elsewhere needs `CAP_SYS_PTRACE` or
  `ptrace_scope=0`. `dbgmcp doctor` reports which you have.
- **Attaching suspends a live process.** Stopping the session detaches without killing it -- that is
  asserted by a test, because the difference is one boolean deep inside teardown. Its output still
  goes wherever it was already going, so `get_session_output` has nothing to show for an attached
  session.
- **Absence is classified by the backend, not guessed from the text.** A transcript reports values
  it has under `values` and the rest under `absent`, with the reason. A gap is never read as a zero,
  and a counter honestly reaching zero is not reported as having gone missing.
- **`findings` sees shape, not correctness.** It notices a trend that broke, a value that became
  empty, one that froze, a step out of line with the rest. It cannot tell a wrong number from a
  right one, because nothing in a transcript says what the answer should have been.
- **Repeated calls in one execution unit are spliced into one series.** Concurrent units are kept
  apart, and a reset back to the starting value is not read as a reversal, but two sequential calls
  of the same function still share a series.
- **No `diff_runs`.** Comparing a passing run with a failing one is what turns "wrong value" into a
  located bug, and it is the next thing to build.
- **`trace_mode` is `auto_continue`, never `buffered`.** Genuinely non-stop tracing needs Delve's
  eBPF uprobes: Linux-only, privileged, and not enabled here. On macOS the gdbserial backend
  cannot do it at all.
- **No safety guard on evaluation.** Delve does not call functions by default, which the
  conformance suite verifies, so the worst of the risk is absent rather than defended against.
- **Watchpoints are hardware watchpoints.** At most four at once, each bound to the stack frame it
  was set in. The variable must already be in scope: stopping at a function's entry is before its
  locals are declared.
- **Goroutine ancestry costs performance** and is off unless `record_ancestry` is set on the
  session.
- **Output is a bounded ring.** A very chatty program loses its oldest lines; `dropped` says how
  many, so silence is never mistaken for a quiet program.

## Keeping the capability claims honest

`describe_backend` is only worth reading if it is true, so every field of it is exercised by a
single suite in `internal/backend/conformance`, run against each backend. A declared capability
must demonstrably work; an undeclared one must refuse with a message naming what is missing.

This is not decoration. A declaration is one line and a capability is not, so the two drift apart by
default; the suite is what stops them.

## Development

```bash
go test ./...        # includes live tests that launch a real dlv against testdata/buggy
dbgmcp doctor
```

The live tests skip when Delve is absent rather than failing. There are no mocked debuggers in
this repository on purpose: a mocked debugger proves nothing about whether this can debug.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Two rules are load-bearing rather than
stylistic: no mocked debuggers, and a declared capability must be exercised by
the conformance suite.

## License

[MIT](LICENSE).
