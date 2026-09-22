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

## What this is for

An internal platform for runtime debugging, wired into how products are built and
checked — not a product sold on feature count. That changes the target.

**Coverage is a requirement, not a vanity metric.** The estate is JavaScript/TypeScript,
Go and Python, with Ruby being phased out. Go is roughly a third of it. A Go-only
debugger serves a third of the products, which is not a platform.

One DAP backend covers Python, JavaScript/TypeScript and Ruby — Ruby arrives free
through `rdbg` even though it is on the way out. Three backends then cover the whole
estate: Delve (done), DAP, and later CDP for the browser.

**The integration layer is the part that does not exist elsewhere.** Debugger
primitives are commodity: [mcp-debugger](https://github.com/debugmcp/mcp-debugger)
covers eight languages over DAP, and
[Microsoft's DebugMCP](https://github.com/microsoft/DebugMCP) drives VS Code's. What
none of them do is "a test failed in CI, so the value trace is attached to the pull
request" or "QA saw it once, and handed over evidence rather than repro steps". That
is where the work is.

What carries over from the product framing, because it matters more for an internal
tool rather than less:

- **Depth below DAP.** Per-goroutine hit counts, expressions evaluated on the
  breakpoint side and execution-unit ancestry do not cross that protocol. Delve stays
  on its native API; the DAP backend declares the reduced capability honestly.
- **Machine-checked honesty.** A capability declared and not exercised is worse than
  none, because the agent trusts it. An internal platform that lies costs more than a
  product that does, because nobody shops elsewhere.
- **Round trips as a measured cost.** Measured against mcp-debugger on the same task:
  27 calls and 9.1 KB versus 3 calls and 1.6 KB. On a working context of 40K tokens
  that is roughly $5 against $0.60 per investigation.

## The four pains, and what answers each

| Pain | What answers it | Status |
|---|---|---|
| "Cannot reproduce it" | Evidence captured at the moment of observation: attach to the stand, probes in the service's own code while QA drives the UI | attach works locally; stands need path mapping |
| "Root cause takes hours" | `explain_value`, `diff_runs`, `findings` | `explain_value` done; the other two are next |
| "Flaky tests" | Honestly, the weakest case — see below | partial |
| "Post-release regressions" | `attach` to a live process, goroutine/thread state, ancestry | works locally |

**On flaky tests, plainly:** outside Linux-with-eBPF, tracing stops the process at every
hit, so observing a race changes the race. Conditions and hit counts reduce the number
of stops, and that helps, but this tool will not be the answer to a timing bug it
perturbs. Saying so is cheaper than discovering it during an incident.

## Near term, in the order that unblocks the rest

### 1. The DAP backend — Python, JavaScript/TypeScript, Ruby

Nothing else is worth building first, because everything else would serve a third of
the estate. One implementation, three runtimes: `debugpy`, `js-debug`, `rdbg`.

It is also the test the neutral model has never had. An abstraction with one
implementation is usually wrong, and the cost of finding that out rises with every
tool added on top of it. Capabilities are the safety valve: DAP has data breakpoints
but no per-unit hit counts and no breakpoint-side expression recording, so the backend
declares `trace_mode: suspend_only` and the tool layer degrades rather than lies.

### 2. `findings` — the server reads the transcript

A transcript comes back with the server's own reading: a value that was monotonic and
stopped being, `nil` where it never appeared, an iteration count that differs between
runs, a probe that never fired, a panic correlated with the last recorded values.

This is what makes the CI integration worth anything. A transcript nobody reads is not
evidence. Language-neutral by construction, because findings are computed from the
transcript rather than from the debugger.

### 3. `diff_runs` — passing input against failing input

Run twice under the same probes, return the first point where the transcripts diverge.
Directly attacks "root cause takes hours", and is the only honest lever available
against flaky tests: compare a passing run with a failing one rather than trying to
watch the race.

### 4. Non-interactive CI mode

One command, no agent in the loop: rerun a failing test under the debugger with probes
derived from the failure, and write an artifact a human or a reviewer can read. This is
what "wired into how products are built" actually means, and it needs (2) and (3) to
produce anything worth attaching.

### 5. QA on the web: Playwright alongside the debugger, and attach to stands

Playwright drives the flow while breakpoints sit in the service's own code — "run the
checkout flow and stop when `total` goes negative" is a question neither tool answers
alone. Needs container-to-host path mapping, which is modelled and unexercised, and a
guard against an agent suspending a stand somebody else is using.

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
