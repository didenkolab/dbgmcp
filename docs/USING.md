# Debugging with an agent

Two tools, one contract. This is how to set them up and which to reach for.

## What each one is

**The [JetBrains plugin](https://github.com/didenkolab/jetbrains-debugger-mcp-plugin)** exposes the
debugger of a running IDE. It sees what the IDE sees: run configurations, usages, quick fixes,
every language the IDE has a debugger for.

**`dbgmcp`** drives a debugger itself, with no IDE anywhere. Go through Delve, Python and
JavaScript/TypeScript through DAP. It runs in a terminal, in CI, over SSH.

Twenty tool names are shared. An agent that learned one has learned the other; the companion skill
is written against the shared names.

## Which one

| | plugin | dbgmcp |
|---|---|---|
| You have the project open in an IDE | ✅ | works too |
| A pipeline, a container, a machine with no GUI | ✗ | ✅ |
| A language the IDE debugs and this does not (PHP, C#, Ruby, native) | ✅ | ✗ |
| "Why did this value end up wrong" in one call | ✗ | ✅ `explain_value` |
| Break when a value changes | ✗ | ✅ `set_watchpoint` (Go) |
| Find usages, apply a quick fix | ✅ | ✗ |
| Attach to a process already running | ✗ | ✅ (local) |

Rule of thumb: **IDE open and the question is about code — the plugin. No IDE, or the question is
about values over time — `dbgmcp`.**

## Setting up

### dbgmcp

```bash
go install github.com/didenkolab/dbgmcp/cmd/dbgmcp@latest
dbgmcp doctor            # reports the platform and the debuggers it can find
```

Its own debuggers are separate installs, and `doctor` names the command for each:

```bash
go install github.com/go-delve/delve/cmd/dlv@v1.27.2        # Go
python3 -m pip install debugpy                              # Python
# JavaScript: a GitHub release asset, not an npm package
mkdir -p ~/.cache/dbgmcp && cd ~/.cache/dbgmcp \
  && gh release download v1.117.0 -R microsoft/vscode-js-debug \
       -p 'js-debug-dap-*.tar.gz' -O js-debug.tar.gz \
  && tar xzf js-debug.tar.gz && rm js-debug.tar.gz
```

Register it once, for every project:

```bash
claude mcp add dbgmcp --scope user -- "$(go env GOPATH)/bin/dbgmcp"
```

### The plugin

Install it into the IDE, open the project, and the tools appear while the IDE is running.

### The skill

Both are driven better with the companion skill, which teaches the agent the economics rather than
the API — read capabilities before planning, set breakpoints before the first resume, trace a loop
instead of stopping in it.

```bash
mkdir -p ~/.claude/skills/runtime-debugging
cp skill/SKILL.md ~/.claude/skills/runtime-debugging/SKILL.md
```

## The loop

```
list_debug_targets        what can be run here        (dbgmcp)
list_run_configurations   the same question           (plugin)
start_debug_session       stops BEFORE the first instruction
set_breakpoint            set them all now, while nothing has run
resume_execution
wait_for_pause            location, stack, variables, source, output — in one reply
evaluate_expression       test the hypothesis against live state
```

Three things worth knowing before the first session, because they are where time goes:

**Read `describe_backend` first.** Capabilities differ by runtime and by platform. Only Go has
watchpoints and per-unit hit counts. Planning against what is there beats discovering it by failing.

**`wait_for_pause` already answered the next three questions.** Following it with `get_variables`
and `get_stack_trace` spends calls on data you have.

**Do not stop in a loop.** `trace_execution` records the expressions you name at every hit and
returns the whole run. A thousand iterations cost one call rather than a thousand.

**When you have a working case, compare instead of hunting.** `diff_runs` takes an input that works
and one that does not, runs both under the same probes and returns the first place they stopped
agreeing. Reach for it the moment you can name a passing case: a single transcript can show you a
value, but only a second run can tell you the value is wrong.

```json
{
  "mode": "test", "target": "./internal/pricing", "work_dir": "/abs/path",
  "run_a": {"label": "passing", "test_run": "TestOrdinaryCustomer"},
  "run_b": {"label": "failing", "test_run": "TestLoyalCustomer"},
  "probes": [{"file": "/abs/path/internal/pricing/price.go", "line": 35, "record": ["price"]}]
}
```

Read three fields before acting on the answer. `first` is the divergence to look at — the rest are
usually its consequences. `compared` being zero means nothing was lined up, which is not the same as
the runs agreeing. And `ambiguous_units`, when present, means more than one goroutine or thread
reached a probe, so the two runs' units were matched by the order they arrived rather than by
identity: treat a divergence there as a lead, not a finding.

## Without an agent

A failing test in CI has no conversation to hold:

```bash
dbgmcp trace -dir . -mode test -target ./internal/billing \
  -test TestSubtotal \
  -probe internal/billing/cart.go:26=total,i \
  -format md -out trace.md
```

It re-runs the target under the debugger, records the values, reads the transcript and writes a
report: what it noticed first, the values under that, the full table last. It exits zero even when
it notices something — the failing test fails the build, not the diagnostic.

A suite kept behind build tags needs them named, or it cannot be built at all:

```bash
dbgmcp trace -tags "integration devsecrets" -test TestCharge \
  -probe internal/billing/charge.go:88=amount
```

The comparison has the same shape. This is the one to attach to a review when a test passes in one
place and fails in another:

```bash
dbgmcp diff -dir . -target ./internal/pricing \
  -test-a TestOrdinaryCustomer -label-a passing \
  -test-b TestLoyalCustomer    -label-b failing \
  -probe internal/pricing/price.go:35=price \
  -format md -out diff.md
```

The report leads with the first divergence and the values on both sides, under your own labels. Test
shuffling is always off for a diff and is not a flag — two runs of a shuffled suite run different
tests, so the divergence found would be the shuffle.

## When it does not work

`dbgmcp doctor` first: it reports the platform, the debugger in use and its version, and on Linux
whether `ptrace_scope` allows attaching to a process you did not start.

Then the honest limits, so time is not spent against them:

- **`findings` sees shape, not correctness.** It notices a trend that broke or a value that froze.
  It cannot tell a wrong number from a right one, because nothing in a transcript says what the
  answer should have been.
- **Tracing perturbs timing** everywhere except Linux with eBPF. For a race, the observation
  changes what it observes.
- **Attaching is local only**, and on Linux is bounded by Yama.
- **Watchpoints are hardware watchpoints**: about four, bound to a stack frame, and the variable
  must already exist — stopping at a function's entry is before its locals are declared.
