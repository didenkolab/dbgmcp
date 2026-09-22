# Roadmap

## Where this stands

Working today, verified by tests that launch real processes:

- **Go, headless.** Launch (`test` / `debug` / `exec`), breakpoints by symbol or line, conditions,
  hit-count conditions, stepping, watchpoints, evaluation, variable mutation, goroutines with
  blocked reasons, goroutine ancestry, source context, debuggee stdout/stderr.
- **`trace_execution`** — a whole run's worth of recorded expressions in one call.
- **`explain_value`** — every change to one value, with the write that made it wrong.
- **`list_debug_targets`** — packages and test names from source, executing nothing.
- **Capabilities as data**, backed by a conformance suite that fails on a claim it cannot verify.
- 27 tools. Acceptance test finds a planted bug in 6 MCP calls, and the count is asserted.

## The competitive position, stated honestly

The IDE-less agentic debugger space is occupied and not by amateurs.
[mcp-debugger](https://github.com/debugmcp/mcp-debugger) covers seven or eight languages over DAP
and runs anywhere Node does. [Microsoft's DebugMCP](https://github.com/microsoft/DebugMCP) drives
VS Code's debugger from an embedded MCP server.
[Govinda-Fichtner/debugger-mcp](https://github.com/Govinda-Fichtner/debugger-mcp) does the same in
Rust. A value-history tool of the same shape as `explain_value` already exists as
`debug_trace_value` in qwen-dap-mcp.

**Breadth is taken.** Racing eight languages against projects that already have them, with zero
users and Microsoft in the field, is a race to arrive second.

What is not taken:

1. **Depth below DAP.** Every competitor speaks DAP, so every competitor inherits its ceiling.
   Per-goroutine hit counts, watchpoints, execution-unit ancestry and expressions evaluated on the
   breakpoint side do not cross that protocol. They are the reason this one speaks Delve directly.
2. **Machine-checked honesty.** Others document their limits in prose. Here a capability is a value
   the agent reads, and a conformance suite fails the build when a claim is not backed. That suite
   has already caught one false claim of ours, which is the argument for it.
3. **Round trips as a measured cost.** The acceptance test asserts the call count, so a change that
   makes the tools harder to use fails rather than quietly degrades.

So the bet is: **a debugger that answers questions, on one runtime, better than a protocol bridge
answers them on eight.**

---

## Near term — make the bet visible

### 1. `findings` — the server reads the transcript

A transcript comes back with the server's own reading: a value that was monotonic and stopped
being; `nil` or zero where it never appeared before; an iteration count that differs between runs;
a probe that never fired; a panic correlated with the last recorded values.

Cheap, language-neutral by construction (it reads the transcript, not the debugger), and it is the
difference between handing over data and handing over an answer.

### 2. `diff_runs` — passing input against failing input

Run the target twice under the same probes, return the **first point where the transcripts
diverge**. Bug localisation becomes one line instead of four hundred lines of log. Builds entirely
on `trace_execution` plus the `findings` comparison machinery.

### 3. Streaming traces

`trace_execution` blocks until the run ends. Hits should arrive by cursor while it runs, the way
`get_session_output` already works. Turns a long-running service from "unusable with this tool"
into "watchable".

### 4. Attach to a running process

Debug a service that is already up — a `docker-compose` stand, a remote host. The biggest practical
unlock for real work, and the one that drags in the most: container-to-host path mapping (the
`PathMapping` model exists and is unexercised), and a guard against an agent suspending a service
somebody else is using.

---

## Medium term — prove the abstraction, then spend it

### 5. A second backend, as a test rather than a feature

A thin Python/debugpy backend over DAP, implemented only as far as `wait_for_pause` and
`get_variables`. It is DAP, it has threads rather than goroutines, and it has no watchpoints — it
presses exactly where the interface might crack. An abstraction with one implementation is usually
wrong, and the cost of finding that out rises with every tool added.

Not a claim of Python support. A test of the neutral model.

### 6. Safety guard on evaluation, per backend

Delve does not call functions by default, which the conformance suite verifies, so the worst risk
is currently absent rather than defended against. A backend that does call functions needs a guard,
and the plugin's JVM blocklist is useless here.

### 7. Ship it properly

Binaries per platform, `brew`, the companion skill installable in one step, a README that leads with
the six-call acceptance run rather than a feature list.

---

## Longer term — the parts that are not designed yet

### 8. Frontend debugging over CDP

Wanted, and deliberately not started. The model is already held open for it: `Breakpoint.Kind` is an
open field so DOM, event-listener and network breakpoints can be added without a breaking change,
and `PathMapping` generalises Delve's substitute-path rules and a browser's source maps into one
idea.

The direction is CDP directly rather than a DAP adapter — same reasoning as Delve: DOM,
event-listener and XHR breakpoints plus console and network streams do not survive a DAP adapter.
The combination worth building for is a Playwright script driving the flow while breakpoints sit in
the application's own code: "run the checkout flow and stop when `total` goes negative" is a
question neither tool answers alone.

Nothing in this section has been verified against source. It is a direction, not a design.

### 9. Buffered tracing where the platform allows

`trace_mode: buffered` — hits accumulating inside the debugger with the process never stopping —
needs Delve's eBPF uprobes: Linux only, privileged. Worth an opt-in on the Linux CI leg, because it
is the only configuration where tracing does not perturb what it measures. Everywhere else,
including macOS, `auto_continue` is the honest answer and the response says so.

---

## Not doing

- **Chasing language count.** Losing race, and it would cost the depth that is the whole argument.
- **Reverse / time-travel debugging.** Needs Mozilla `rr`: Linux/x86 only, so it will never work on
  the primary development machine. Not worth a capability that cannot be tested where it is written.
- **Editing code through the debugger.** Runtime inspection and mutation, yes. Source editing is
  what the agent's other tools are for.
