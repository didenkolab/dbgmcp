# dbgmcp

[![test](https://github.com/didenkolab/dbgmcp/actions/workflows/test.yml/badge.svg)](https://github.com/didenkolab/dbgmcp/actions/workflows/test.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/didenkolab/dbgmcp.svg)](https://pkg.go.dev/github.com/didenkolab/dbgmcp)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Debug programs with breakpoints, from an AI agent, **without an IDE**. Go, Python and JavaScript/TypeScript.

## Why

Reading code tells you what a program is meant to do. It is silent on what it did. Every conclusion
drawn from reading is taken on trust -- and an agent reading code reaches those conclusions faster,
and states them more confidently, than a person would.

This asks the program instead.

### Three questions, one call each

| The question | The tool |
|---|---|
| What happened across this loop, all thousand iterations of it? | `trace_execution` |
| Why is this value wrong? | `explain_value` |
| It works on this input and fails on that one -- where do they part? | `diff_runs` |

Each is a whole question rather than a primitive. A loop costs one call instead of one call per
iteration, because the debugger evaluates the expressions itself and resumes on its own. A wrong
value comes back as its own history -- what it was, what it became, where, with what stack -- rather
than as somewhere to start guessing.

The third has no substitute. Seven rules read a transcript and notice an anomalous *shape*: a trend
that reversed, a value that froze, a first `nil`. None of them can tell a wrong number from a right
one, because nothing in a single run says what the number should have been. **A second run says it.**
The passing run is the specification, and the first place the failing run departs from it is the
answer.

### What it costs to use

The benchmark this project holds itself to is an acceptance test that locates a wrong value at
runtime in **six MCP calls**, and fails if that number grows. Round trips are the budget: an agent
that spends twenty calls on one variable has usually lost the thread by the tenth.

### The same vocabulary with and without an IDE

Twenty-four tool names are shared verbatim with our [JetBrains debugger
plugin](https://github.com/didenkolab/jetbrains-debugger-mcp-plugin), so one agent and one companion
skill drive an IDE session and a headless one without branching on which they got.

### And nothing claims a capability it does not have

`describe_backend` reports what the debugger underneath can actually do. That report is not
documentation: one conformance suite runs against every backend and checks it **both ways** -- a
declared capability has to work, an undeclared one has to refuse and name what is missing. A
declaration is one line and a capability is not, so the two drift apart by default.

---

`dbgmcp` is an MCP server that drives a headless [Delve](https://github.com/go-delve/delve) over
its native RPC API. It runs anywhere Go runs -- a terminal, a container, CI -- and needs no editor.

```
agent --MCP stdio--> dbgmcp --rpc2 over unix socket--> dlv --headless --> your program
```

**New here?** [docs/USING.md](docs/USING.md) covers both this and the IDE plugin: what to install,
which to reach for, and the three things that decide how long a session takes.

## Install

```bash
go install github.com/didenkolab/dbgmcp/cmd/dbgmcp@latest
dbgmcp doctor        # checks that Delve is present and reachable
```

Delve is required and pinned to v1.27.2:

```bash
go install github.com/go-delve/delve/cmd/dlv@v1.27.2
```

### Register it with your agent

It speaks MCP over stdio, so the command is the whole configuration:

```bash
claude mcp add dbgmcp -- dbgmcp
```

Or, written out — the same shape every MCP client uses:

```json
{
  "mcpServers": {
    "dbgmcp": { "command": "dbgmcp", "args": [] }
  }
}
```

### Settings

There are no config files. Four environment variables exist, and the first three
are the difference between a runtime working and appearing to be broken:

| Variable | What it is for |
|---|---|
| `DBGMCP_DLV` | Absolute path to `dlv`. Needed when it is not on `PATH` — which is usual, because `go install` puts it in `GOPATH/bin` and a GUI-launched agent inherits a much shorter `PATH` than your shell. Checked first, before `PATH` and `GOPATH/bin`. |
| `DBGMCP_PYTHON` | Absolute path to the interpreter to debug with, normally your project's virtualenv. **Set this for Python.** Without it the first `python3` on `PATH` is used, and that one rarely has `debugpy` installed — the failure looks like the debugger being broken rather than the wrong interpreter being picked. |
| `DBGMCP_JS_DEBUG` | Directory of Microsoft's standalone js-debug DAP server. **Set this for JavaScript and TypeScript**, unless you unpacked it into `~/.cache/dbgmcp/js-debug`, which is looked at next. There is nothing to `go install`: it ships as a GitHub release asset. |
| `DBGMCP_DAP_TRACE` | Any non-empty value logs the raw DAP conversation. For diagnosing an adapter, not for normal use. |

`dbgmcp doctor` reports what was actually found, so run it after setting these
rather than trusting that they took effect.

Registered for an agent, they go beside the command:

```json
{
  "mcpServers": {
    "dbgmcp": {
      "command": "dbgmcp",
      "env": {
        "DBGMCP_PYTHON": "/path/to/project/.venv/bin/python",
        "DBGMCP_JS_DEBUG": "/path/to/js-debug"
      }
    }
  }
}
```

## Use

### From an agent

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
execution unit, stack, variables and source together, which is most of where the six-call budget
above is won: following it with `get_variables` and `get_stack_trace` spends calls on data already
in hand.

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

**`trace_execution`: a whole run in one call.** Declare where to record and what to record, and get
the transcript back from one call. Delve evaluates the expressions itself at every hit and resumes
on its own, so watching a thousand iterations costs one call, not a thousand.

The saving is **round trips, not observer effect**, and the result says which you got. Without eBPF
-- so on macOS always, and on Linux unless `dlv --ebpf` is enabled and privileged -- Delve stops the
debuggee at every hit and resumes it itself, so the transcript reports `mode: "auto_continue"` and
`perturbs_timing: true`. Genuinely non-stop tracing is `mode: "buffered"`, and it is not available
here. An agent chasing a race reads that from the response rather than from this paragraph.

### From a pipeline

The whole recipe, including a ready step for GitLab CI and GitHub Actions, is in
[docs/CI.md](docs/CI.md). `ci/debug-failing-test.sh` needs no configuration: the
failing test's name, its package and the line it gave up on are already in
`go test` output.


A failing test in CI has no conversation to hold, so tracing is also a command:

```bash
dbgmcp trace -dir . -mode test -target ./internal/billing \
  -test TestSubtotal \
  -probe internal/billing/cart.go:26=total,i \
  -format md -out trace.md
```

A suite kept behind build tags needs them named, or it cannot be built at all — and that is usually
the half that talks to a database:

```bash
dbgmcp trace -tags "integration devsecrets" -test TestCharge \
  -probe internal/billing/charge.go:88=amount
```

Test-order randomisation is switched off by default, because a shuffled run puts a different test
where the probe expects one. Pass `-shuffle` to leave it on.

Comparing two runs is a command too, and it is the one to reach for when a test passes in one place
and fails in another:

```bash
dbgmcp diff -dir . -target ./internal/pricing \
  -test-a TestOrdinaryCustomer -label-a passing \
  -test-b TestLoyalCustomer    -label-b failing \
  -probe internal/pricing/price.go:35=price \
  -format md -out diff.md
```

The labels are the reader's, and they appear in the report rather than "the first run". Randomisation
is always off here and is not a flag: two runs of a shuffled suite execute different tests, so the
divergence found would be the shuffle.

And the third question is a command too, so a script reaches all three:

```bash
dbgmcp explain -dir . -mode test -target ./internal/billing \
  -test TestSubtotal -scope billing.Subtotal -expr total \
  -format md -out why.md
```

The report leads with the last change -- where the value became what it is -- and says whether the
history is complete. That distinction matters: one cut short by a budget or a timeout can be missing
the write that explains everything, and a reader who is not told draws a confident wrong conclusion.

It re-runs the target under the debugger, records the expressions, reads the transcript and writes a
report: what was noticed first, the values it rests on next, the full table last. It exits zero even
when it notices something -- the failing test fails the build, not the diagnostic, because a
diagnostic that can break a pipeline stops being run.

**It reads the transcript for you.** `trace_execution` comes back with `findings`: a value that had
been climbing and reversed, one that had always been present and arrived empty, one that changed
every iteration and quietly stopped. Each carries the values it rests on, and each is an
observation rather than a verdict -- the reader draws the conclusion.

These notice an anomalous *shape*, not a wrong *value*. No rule can tell a wrong number from a
right one without knowing the expected answer -- which is what the next tool is for.

**`diff_runs`: two runs, and the first place they part.** Give it an input that works and one that
does not, with the same probes, and it starts, traces and tears down both runs itself, then reports
the earliest point they stopped agreeing. One call.

The passing run is the specification. `findings` cannot supply one -- nothing in a single transcript
says what a value should have been -- so this is the tool that turns "the number is wrong" into a
file and a line. It separates five kinds of divergence, because they send a reader to different
places: a differing value, a value present in one run and absent in the other (usually a branch not
taken), a differing number of hits (a different path), a probe reached in only one run, and a probe
reached by a different number of execution units (a difference in concurrency rather than in data).

Two things it refuses to fake. Execution units are matched between the runs by the order they
arrived at a probe, never by id -- a goroutine id is assigned within one run, and comparing ids
across two reports every single series as missing. Where more than one unit reached a probe, that
matching is a guess and the reply says so in `ambiguous_units`, because a debugger that is
confidently wrong about a race is worse than one that says it cannot tell.

**It can see what the program printed.** `get_session_output` returns the debuggee's stdout and
stderr, with a cursor for tailing. Outside an IDE there is no console, so without this a panic
message -- often the shortest path to the answer -- would be invisible.

**It finds its own targets.** `list_debug_targets` reads the project and reports the main packages
and every test function by name. An IDE plugin can list run configurations because a human made
them; with no IDE there is nothing to list, so they are derived from the source instead -- without
executing anything.

**Where the two surfaces differ, they differ honestly.** Of the tools not shared with the plugin, it
has what only an IDE can do -- `find_usages`, the quick fixes, run configurations -- and this server
has what only a debugger it drives itself can do: `describe_backend`, `explain_value`,
`set_watchpoint`, `get_unit_ancestors`, `diff_runs`, `list_debug_targets`.

## Known gaps

- **Attaching to an optimised binary gets you much less than attaching to one built for
  debugging.** Measured, not assumed: a short function is inlined and has no symbol to break on,
  lines inside it are gone, a line whose call was eliminated is gone, and variables on the lines
  that do survive are often held in registers and come back `unreadable`. Probing by file and line
  does not rescue this. To debug a running service properly, build it with `-gcflags=all=-N -l`.
  The transcript says `unreadable` rather than showing an empty value, so the limit is visible
  rather than silent.

Stated plainly, so nobody mistakes the test suite for more than it is.

- **Go, Python and JavaScript.** Go goes through Delve's native API; Python and JavaScript through
  DAP, one implementation with a profile each. TypeScript runs through the JavaScript profile with
  source maps on. Ruby is another profile and is not written.
- **Capability sets differ, and are reported rather than assumed.** Only Go has watchpoints,
  per-unit hit counts, ancestry and breakpoints by symbol. `describe_backend` says so per runtime,
  and the conformance suite runs against every backend, checking the refusals as well as the
  support.
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
- **`diff_runs` matches execution units by arrival, not identity.** A unit id is assigned within a
  run, so the two runs' ids cannot be compared; units are aligned by the order they reached the
  probe instead. With one unit that is exact. With several it is a guess, reported in
  `ambiguous_units` rather than presented as settled, because two runs interleave differently.
- **`diff_runs` sees only what the probes see.** Two runs that agree everywhere they are watched are
  reported as agreeing at those probes, not as identical, and `compared: 0` means nothing was lined
  up rather than that the runs matched.
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

**What the suite does not reach, stated so green is not mistaken for covered.** `attach` is skipped
on every hosted runner -- neither the Linux nor the macOS leg permits taking control of a process it
did not start -- so the one path that touches somebody else's running process is exercised only on a
developer's machine. Its safety property, that detaching leaves the process alive, is asserted by
`TestLiveAttachLeavesTheProcessRunning`; run it locally before trusting attach against anything you
care about. Two further skips are honest rather than missing: stepping with a watchpoint where
watchpoints are not declared, and a `nil` error literal the macOS backend will not evaluate.

## Development

```bash
go test ./...        # includes live tests that launch a real dlv against testdata/buggy
dbgmcp doctor
```

The live tests skip when Delve is absent rather than failing. There are no mocked debuggers in
this repository on purpose: a mocked debugger proves nothing about whether this can debug.

**On a constrained macOS machine, run the suite a group at a time.** `go test ./...` runs packages
in parallel, and on a hosted macOS runner several packages each driving a debugger at once wedged the
machine past `go test -timeout`, past the CI step timeout, and only ended when the whole job was
killed -- which is also why it left no log to read. Running the groups in sequence, as
`.github/workflows/test.yml` now does, passes reliably including the Python and JavaScript suites:

```bash
go test ./internal/model/... ./internal/findings/... ./internal/diffruns/... ./internal/discover/... ./internal/session/... ./internal/backend/... -count=1
go test ./internal/backend/delve/... -count=1
go test ./internal/backend/dap/... -count=1
go test ./internal/mcpserver/... ./internal/tools/... -count=1
```

A developer machine with plenty of cores runs `go test ./...` without trouble, so this is about
constrained machines rather than about the tests being wrong.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Two rules are load-bearing rather than
stylistic: no mocked debuggers, and a declared capability must be exercised by
the conformance suite.

## License

[MIT](LICENSE).
