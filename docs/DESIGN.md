# Runtime debugging over MCP, without an IDE — design

**Date:** 2026-09-22
**Status:** the design this server was built from. Where the code has since moved on, the
code is authoritative and `docs/ROADMAP.md` records the direction.
**Relationship to the JetBrains plugin:** the tool-name contract is shared with our
[jetbrains-debugger-mcp-plugin](https://github.com/didenkolab/jetbrains-debugger-mcp-plugin) plugin, so one agent and one companion skill work with an IDE and
without one. It began as a fork of hechtcarmel/jetbrains-debugger-mcp-plugin.

## Problem

The JetBrains plugin gives an AI agent a debugger, but only where a JetBrains IDE is running with
the project open. That excludes CI, containers, remote hosts, and any agent session without a GUI.
This project gives an agent breakpoint-level runtime debugging with no IDE at all — and does it
behind one language-neutral contract, so the same agent behaviour carries from one runtime to the
next.

## Shape of the thing

A single static Go binary, installed with `go install`, speaking MCP over stdio (or HTTP). It
shares no code with the JetBrains plugin — it shares the **tool-name contract**, so one companion
skill (shipped with v1) and one agent work against both.

### Dependencies pinned

MCP via the official `github.com/modelcontextprotocol/go-sdk` v1.8.0. Delve via its own typed
client, `github.com/go-delve/delve/service/rpc2` v1.27.2 (see "The Go backend" below). An
`sdk_assumptions_test.go` pins what the server relies on the SDK doing, playing the role
`McpSdkAssumptionsTest` plays in the plugin.

Three layers, and the value is in the boundaries:

```
                    agent
                      |  MCP - one language-neutral tool contract
        +-------------v--------------+
        |  Tool layer                |  names shared with the JetBrains plugin
        +----------------------------+
        |  Analytics layer           |  transcripts, findings, explain_value, diff_runs
        |  (language-neutral)        |  operates on the neutral model only
        +----------------------------+
        |  Backend interface         |  + Capabilities, as data
        +--+-------------------+-----+
           |                   |
      delve (Go)           DAP (Python, Node, Rust, PHP, ...)     later: JDWP, CDP
```

### 1. The contract is the neutral model, not the protocol

The domain is described once — `Session`, `Breakpoint`, `Probe`, `Hit`, `Frame`, `Variable`,
`ExecUnit`, `TraceTranscript`, `OutputChunk` — and every backend maps into it. The analytics layer
never sees Delve or DAP.

**`ExecUnit` rather than picking a side between "thread" and "goroutine".** Go has goroutines,
Python has threads plus asyncio tasks, Node has one thread and an async stack. One neutral concept
with a `kind` label keeps the tool single (`list_execution_units`) while letting each backend name
its units honestly.

**`Breakpoint.kind` is open from day one**, not a closed `line | watch` pair. Browsers can break on
a DOM mutation, on an event listener firing, or on a network request matching a URL — kinds with no
backend analogue at all. A closed enum would make adding them a breaking change for every client,
so the model carries `kind` plus a backend-specific selector from the first commit. v1 implements
`line` and `watch`; the others are declared through capabilities and rejected with a precise
message until a backend supplies them.

**`PathMapping` is a neutral concern, not a Delve field.** Delve's `substitutePathRules` and a
browser's source maps are two implementations of one idea: the agent names a location in the source
it can read, and the backend resolves it to whatever the runtime actually loaded. Modelling this as
a Go detail would force a retrofit the moment a backend with source maps appears.

### 2. Capabilities are data, not prose

The plugin currently handles this with a prose table in `CLAUDE.md` ("Limited Support: Rust, C++,
..."). That is a documentation answer to a machine problem: the agent learns the limit from text it
may not have read, and cannot check it.

Instead every backend declares, as a value the agent can read:

| Capability | Values |
|---|---|
| `watchpoints` | `full` / `none` |
| `trace_mode` | `buffered` / `auto_continue` / `suspend_only` |
| `hit_counts` | `per_unit` / `total` / `none` |
| `eval_calls_functions` | `yes` / `guarded` / `no` |
| `set_variable` | `full` / `primitives` / `none` |
| `breakpoint_by_symbol` | `yes` / `no` |
| `ancestry` | `yes` / `no` |

`trace_mode` has three values, each a different deal for the agent: `buffered` — hits accumulate
inside the debugger, the process never waits (Delve with eBPF, Linux, privileged only); `auto_continue`
— the server resumes after each hit with no agent round-trip, but each hit still costs a stop, and
the response carries `perturbs_timing: true` (Delve on macOS, Delve on Linux without eBPF, every DAP
backend); `suspend_only` — every hit needs the agent to resume it explicitly (reserved; no v1
backend reports it).

Exposed by `describe_backend` (which takes a `backend` argument, since several backends live in one
binary and the agent may ask before any session exists) and echoed in session status. An
unsupported tool returns the name of
the missing capability and the nearest alternative, not an opaque failure. **The agent plans
against capabilities instead of discovering limits by failing into them.** This is the difference
between "works everywhere" as a fact and as a slogan.

### 3. Analytics degrade, they do not disappear

`trace_execution` reports which of the three `trace_mode` values it ran under. On `buffered` the
process never waits. On `auto_continue` and `suspend_only` it does, and the response says so through
`perturbs_timing: true` in the same shape either way — the agent reads one field, not a backend
name, to know whether timing was disturbed. `explain_value` uses watchpoints where they exist, and
falls back to probes at write sites where they do not — worse, slower, and labelled as the
fallback.

### 4. Go/Delve first, because it is the richest backend

Designing the neutral model against the richest backend yields an abstraction that DAP backends
enter as a degraded case. The opposite order — model DAP first, then try to surface Delve's extras
— yields an abstraction that cannot express them. That is the standard lowest-common-denominator
failure, and it is named here so we do not walk into it.

### 5. A second backend as a test of the abstraction

An abstraction that has never had a second implementation is usually wrong. The last step of v1 is
therefore a **thin Python/debugpy backend, implemented only as far as `wait_for_pause` and
`get_variables`**. It is DAP, it has threads rather than goroutines, and it has no watchpoints — it
presses exactly where the interface might crack. Python is not claimed as a supported runtime in
v1; this is a test, not a feature.

## The Go backend

```
agent --MCP--> server --rpc2 over unix socket--> dlv --headless --api-version=2 --> debuggee
                  |                                  (one per session)
                  +-- session store: N sessions, each with its own dlv and socket
```

**Talk to Delve through its own typed client**, `github.com/go-delve/delve/service/rpc2.RPCClient`,
not hand-rolled JSON-RPC, so a Delve bump fails at compile time. This is the role
`McpSdkAssumptionsTest` plays in the plugin: pin the assumption where a machine can check it.

**The server owns the Delve lifecycle.** `dlv` runs with `--accept-multiclient` so it survives a
client reconnect, and is killed on `stop_debug_session`, on server exit, and on loss of stdio.
`Report.md` records fifteen orphaned processes pinned at ~450% CPU for 27 hours after a test run
whose cleanup never ran; that class of bug is closed by construction, not by discipline.

**Provision `dlv`, do not vendor it.** Look in `PATH` and `$GOPATH/bin`, run `dlv version`, and use
that binary only if its version equals the `rpc2` client version the server was compiled against —
anything else is an API-mismatch risk, not a convenience. Otherwise run
`go install github.com/go-delve/delve/cmd/dlv@v1.27.2` into a server-owned directory (under the
user cache dir) and use that. `describe_backend` reports the `dlv` path and version actually in
use. The server never passes `--check-go-version=false`; Delve 1.27.2 supports Go up to 1.27
(`pkg/goversion/compat.go`, `MaxSupportedVersionOfGoMinor = 27`), and if Delve refuses the
toolchain on a newer Go, that refusal is surfaced to the agent verbatim rather than suppressed.
Vendoring `dlv` would yield one file but drags in architecture-specific ptrace code and macOS
code-signing concerns.

**Build the debuggee with `-gcflags=all="-N -l"`**, or the optimiser eats the variables the agent
came to look at. The server does this by default and discloses it in session status, because it
changes the binary under test.

**The server owns the debuggee's stdio, because it owns the `dlv` process.** Output is captured to
a bounded ring buffer per session — default 1 MiB, oldest bytes dropped, `truncated: true` reported
once dropping starts. Delve's `-r`/`--redirect` flag exists for redirecting target stdio; whether
the server inherits `dlv`'s own pipes or uses `--redirect` is decided by whichever proves reliable
in the first live test — an implementation question, not a design one.

### Why Delve's native API rather than its DAP mode

Verified against Delve v1.27.2 (`service/api.Breakpoint`, `service/rpc2.RPCClient`). None of this
is expressible in DAP:

| Delve API | What it gives the agent |
|---|---|
| `Breakpoint.Variables []string`, `LoadArgs`, `LoadLocals`, `Stacktrace int` | Delve evaluates expressions, arguments, locals and N stack frames itself **on every hit**, saving a round trip between the server and `dlv` per hit — internal to the server, not visible to the agent as a separate call |
| `Breakpoint.Tracepoint` + `GetBufferedTracepoints()` | tracing **without suspending** — but only under eBPF, and it requires Linux and the privileges that eBPF needs. On macOS the default gdbserial backend's `GetBufferedTracepoints` simply returns `nil` (`pkg/proc/gdbserial/gdbserver.go:387-389`); a variant that panics outright exists in the native darwin implementation but is compiled in only under the `macnative` build tag, which a stock `dlv` does not use. Either way this is `trace_mode: buffered`, and it is not available on macOS at all |
| `Breakpoint.HitCount map[string]uint64`, `HitCond`, `HitCondPerG` | "stop on the 100th hit in this goroutine", with real per-goroutine hit counts |
| `CreateWatchpoint(scope, expr, type)` | break when a variable changes |
| `Ancestors(goroutineID, n, depth)` | the stack of whoever created this execution unit |
| `CreateBreakpointWithExpr(..., substitutePathRules, suspended)` | breakpoints on not-yet-loaded code, plus path mapping — groundwork for containers |

In passing, `Breakpoint.HitCount` closes for free the plugin's documented
`BreakpointHitInfo.hitCount is always 0` gap.

What this buys the agent on macOS specifically, without eBPF and without an IDE: no IDE required at
all; real per-goroutine hit counts (`Breakpoint.HitCount`, `HitCond`, `HitCondPerG`); hardware
watchpoints through debugserver; goroutine ancestry (`Ancestors`); breakpoints by symbol rather than
file/line; per-tool value budgets; and the findings layer built on top of all of it. None of that
depends on `trace_mode: buffered`.

## Tool surface

Names match the plugin wherever the semantics match.

**Mirrored (21):** `start_debug_session`, `stop_debug_session`, `list_debug_sessions`,
`get_debug_session_status`, `set_breakpoint`, `list_breakpoints`, `remove_breakpoint`,
`resume_execution`, `pause_execution`, `step_over`, `step_into`, `step_out`, `run_to_line`,
`wait_for_pause`, `get_variables`, `set_variable`, `evaluate_expression`, `get_stack_trace`,
`select_stack_frame`, `get_source_context`, `trace_execution`.

**Renamed, for the neutral contract:**

- `list_run_configurations` -> **`list_debug_targets`**. No IDE means no run configurations. For Go
  the targets come from `go list -json ./...`; every backend supplies its own discovery. The agent
  asks what can be run here instead of asking the human.
- `list_threads` -> **`list_execution_units`**. See the `ExecUnit` decision above. An earlier draft
  of this design renamed it to `list_goroutines`; that was wrong — it solved the mismatch with a
  word instead of with a model, and it would have broken the shared contract at the first
  non-Go backend.

**Dropped:** `find_usages`, `list_quick_fixes`, `apply_quick_fix`, `execute_run_configuration`.
Without PSI and an inspection engine there is nothing to implement them with. A language server
could supply find-usages; that is a second server and a different product.

**Added:** `describe_backend` (capabilities), `set_watchpoint`, `get_unit_ancestors`,
`get_session_output`. Watchpoints are listed and removed through `list_breakpoints` /
`remove_breakpoint`; they are a kind of breakpoint in the neutral model, not a parallel registry.

`describe_backend` takes a `backend` argument — several backends live in one binary, and the agent
may want to ask before any session exists. It returns the backend's declared capabilities plus
platform, the backend tool's path and version (for Delve: the `dlv` path and version actually in
use, per the provisioning rule above).

`get_session_output` returns the debuggee's stdout/stderr captured by the server since session
start, with `offset`/`limit` (or a `since` cursor) so the agent can tail it without re-reading what
it has already seen. `get_debug_session_status` also carries the last N lines, so the common case —
"what did the program just print" while already inspecting a pause — needs no extra call.

### Cross-cutting

- `set_breakpoint` accepts **either** `file` + `line` **or** `location` as a symbol, when the
  backend declares `breakpoint_by_symbol` (Delve: `pkg.Func`, `Func:12`). This matters more than it
  looks: the agent no longer has to read a file and count lines to break on a function.
- Every value-returning tool takes a budget — `max_depth`, `max_string_len`, `max_array_values`,
  mapped per backend (for Delve, onto `api.LoadConfig`). Without it a single `get_variables` on a
  pointer-rich struct eats the context window.
- Errors follow the plugin: a successful result carrying `isError: true` and human-readable prose,
  with the strings pinned by tests, because they are the only failure signal a client gets.

## What makes it worth building

**Level 1 — `trace_execution` in one call, with `trace_mode` telling the truth.** On macOS — the
author's machine, and the primary matrix leg — Delve's tracepoints run in `auto_continue` mode: the
server resumes after each hit with no agent round-trip, but the debuggee still stops on every hit,
so timing is perturbed exactly as it is for the plugin's version. That is parity with the plugin's
`trace_execution`, not an improvement over it, and the response says so honestly through
`trace_mode: "auto_continue"` and `perturbs_timing: true`. The genuinely non-suspending
`trace_mode: "buffered"` exists only on Linux with `--ebpf` and the privileges that requires; it is
an opt-in on the linux/amd64 CI leg if the runner can run privileged, and otherwise slips to v1.1.
Either way the agent learns which mode it got from the response fields, not from a docstring it may
not have read. **In v1, `auto_continue` version; `buffered` opt-in on CI where available.**

**Level 2 — `findings`: analysis while the run happens.** A transcript comes back with the server's
own reading of it: a value that was monotonic and stopped being; `nil` or zero appearing where it
never did; a probe that never fired; an iteration count that differs between runs; a unit blocked
past a threshold; a panic correlated with the last recorded values. Language-neutral by
construction, because findings are computed from the transcript rather than from the debugger.
**In v1, starting with the cheap and unambiguous rules.**

**Level 3 — `explain_value`.** The agent says "`total` was -1 at line 88, explain". The tool sets a
watchpoint, re-runs the target, and returns the **history of that variable's changes** — where,
from what, to what, with the stack at each write. This is the question people ask a debugger most
often, and an agent cannot assemble it from primitives in a reasonable number of calls. **In v1.1.**

**Level 4 — `diff_runs`.** Given a passing input and a failing one, run the target twice under the
same probes and return the **first point where the transcripts diverge**. Bug localisation becomes
one line of answer instead of four hundred lines of log. **In v1.1.**

Levels 3 and 4 are held back deliberately. Hardware watchpoints number four and are bound to a
stack frame; until live runs show how they actually behave, building the flagship feature on them
would be promising something unverified.

## Frontend, later

Debugging a browser frontend is wanted, and is deliberately not in v1 — it is arguably a larger
project than the Go backend, because target launch, navigation, source maps and DOM-level
breakpoints are all new surface. What v1 owes it is only that the model not foreclose it, which the
`Breakpoint.kind` and `PathMapping` decisions above pay for at close to zero cost.

The direction, to be verified before it is committed to:

- **Speak CDP directly rather than through a DAP adapter**, for the same reason the Go backend
  speaks Delve's native API: the Chrome DevTools Protocol carries DOM, event-listener and XHR
  breakpoints, plus console and network event streams, and a DAP adapter flattens those away.
- **A target gains a `driver`.** Launching a frontend is not starting a process — it is starting a
  dev server, opening a browser and navigating. `list_debug_targets` and `start_debug_session`
  already take per-backend target descriptions, so the shape exists; the driver is what fills it.
- **`findings` gain frontend rules**: console errors, unhandled promise rejections and failed
  requests, correlated with the recorded transcript. This is where the language-neutral analytics
  layer pays off — the rules are new, the machinery is not.
- **The combination worth building for**: a Playwright script drives the flow while breakpoints sit
  in the application's own code. "Run the checkout flow and stop when `total` goes negative" is a
  question neither tool answers alone. The project owner's standing rule is that anything reachable
  through the web is tested through the web, so this is the native shape of the work rather than an
  add-on.

Unlike the Delve claims in this document, nothing in this section has been verified against source.
Treat it as a direction, not a design.

## Testing

The plugin's culture carries over: a **golden tool manifest** with an update flag and a reviewed
diff, **live tests with no mocks** (a mocked debugger proves nothing here), and a **Known gaps**
section in the README from day one.

New, and specific to this project:

- **The acceptance test is the success criterion.** A script drives only MCP tools, the way an agent
  would, against a deliberately buggy program in `testdata/buggy/`, and must name the file and line
  of the bug. The script is written as the minimum tool sequence an agent would need to find it, not
  padded or golfed; the call count is recorded, and a tool change that forces the script to grow is
  treated as a regression that must be justified in the PR.
- **A backend conformance suite.** One suite, run against every backend, that asserts each declared
  capability actually holds and each undeclared one fails with the documented message. This is what
  keeps `describe_backend` honest as backends are added.

Matrix: darwin/arm64 (the author's machine) and linux/amd64 (CI). Both legs have hardware
watchpoints. macOS goes through Delve's gdbserial backend (`debugserver`), which carries a known
workaround for a Mach kernel issue where single-stepping with hardware watchpoints set can send a
spurious exception (`pkg/proc/gdbserial/gdbserver.go`, ~line 862). The conformance suite runs the
watchpoint cases on both legs and records any behavioural difference as a capability value, not as
a footnote.

## Scope of v1

Go, complete. Launch only (`test` / `debug` / `exec`), local only. Plus the neutral model, the
capability system, and the thin Python backend as its test.

Also a v1 deliverable: a companion `SKILL.md`, shipped in the new repository, written against the
neutral tool contract — discover targets, start session, breakpoints, `wait_for_pause`, inspect,
step, plus `trace_execution` and `get_session_output` — phrased so it is also correct against the
JetBrains plugin wherever tool names coincide. No such skill exists today, in this repository or
elsewhere; without it "one companion skill and one agent work against both" is a claim the design
made and nothing shipped.

Out of scope, stated so nobody expects it:

- attaching to an already-running process
- containers and remote hosts (`substitutePathRules` is modelled from day one, not exercised)
- reverse / time-travel debugging — it needs Mozilla `rr`, which is Linux/x86 only, so it will never
  work on the author's arm64 Mac
- `trace_mode: buffered` (eBPF tracepoints) as a guaranteed capability — it is not out of scope as a
  concept, but it is Linux-only, requires `dlv --ebpf` and elevated privileges, and is at most a
  linux/amd64 CI-leg opt-in; everywhere else, including the author's Mac, v1 ships
  `trace_mode: auto_continue`
- Python as a claimed, supported runtime

## Known risks

- Hardware watchpoints: four of them, scoped to a stack frame. On macOS, Delve's gdbserial backend
  (`debugserver`) carries a workaround for a Mach kernel issue where single-stepping over a
  breakpoint while hardware watchpoints are set can trigger a spurious mach exception
  (`pkg/proc/gdbserial/gdbserver.go`, ~line 862); the conformance suite must exercise this path on
  both matrix legs, not assume the workaround is transparent.
- `Ancestors` requires `GODEBUG=tracebackancestors=N` in the debuggee's environment and slows it
  down.
- Compiler optimisation removes variables; mitigated by `-gcflags=all="-N -l"`, which must be
  disclosed because it changes the binary under test.
- Delve's expression evaluation does not call functions by default (`Call` is a separate, riskier
  API). The safety guard must be built per backend — the plugin's JVM blocklist is useless here.
- The capability model is only as good as its conformance suite; a capability declared and not
  tested is worse than no capability model at all, because the agent will trust it.

## Revision notes (2026-09-22)

1. **Level 1 was wrong on macOS.** `GetBufferedTracepoints` returns `nil` on darwin's default
   gdbserial backend (`pkg/proc/gdbserial/gdbserver.go:387-389`; a panicking variant exists in the
   native darwin implementation but is behind the `macnative` build tag a stock `dlv` does not use)
   and is nil on Linux without eBPF tracepoints (`CreateEBPFTracepoint` / `GetBufferedTracepoints`
   over rpc2, not a CLI flag); `dlv trace` without `--ebpf` sets ordinary breakpoints and loops
   `Continue()` client-side, so on the author's machine a Delve tracepoint suspends like a
   breakpoint. Replaced the two-value `non_suspending_trace` capability with three-value
   `trace_mode` (`buffered` / `auto_continue` / `suspend_only`) and corrected every section that
   claimed non-suspending tracing as a v1, cross-platform fact.
2. **Debuggee output had no home.** Neither this design nor the plugin contract returns the
   debuggee's stdout/stderr, and there is no IDE console to fall back on outside an IDE. Added
   `get_session_output`, an output ring buffer owned by the server, and `OutputChunk` to the neutral
   model.
3. **No MCP library was named.** Pinned `github.com/modelcontextprotocol/go-sdk` v1.8.0 as the MCP
   dependency and added an `sdk_assumptions_test.go` commitment, mirroring `McpSdkAssumptionsTest`.
4. **`dlv` version drift was unguarded.** The server compiles against the v1.27.2 `rpc2` client, so
   a mismatched `PATH` binary is an API-mismatch risk; rewrote provisioning to check `dlv version`
   before trusting `PATH`, and to surface Delve's own Go-version refusal (`MaxSupportedVersionOfGoMinor
   = 27` in `pkg/goversion/compat.go`) rather than suppress it with `--check-go-version=false`.
5. **The "companion skill" did not exist.** No `SKILL.md` for this contract exists in this repo or
   the user's global skills directory; moved it from an assumed fact to an explicit v1 deliverable.
6. **Watchpoints-on-macOS was hand-waved.** Delve's gdbserial backend implements hardware
   watchpoints on macOS via `debugserver` (`gdbserver.go:1319-1342`) and carries a documented
   single-step workaround for a Mach kernel issue (`gdbserver.go` ~line 862); replaced the vague
   "watchpoints most of all" with the concrete mechanism and put it in the conformance suite's scope.
7. **The acceptance test's call count measured the script, not the tools.** Reworded so the script
   is defined as the minimum sequence an agent would need, and a forced increase in that count is a
   reviewable regression rather than an unexplained number.
8. **`describe_backend`'s shape was unstated.** Multiple backends can live in one binary, so it
   needs a `backend` argument even before a session exists; specified its return shape (capabilities,
   platform, backend tool path and version) consistently with the Delve provisioning rule in (4).
