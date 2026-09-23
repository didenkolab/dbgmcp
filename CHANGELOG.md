# Changelog

All notable changes are recorded here. This project follows [semantic
versioning](https://semver.org).

## [Unreleased]

### Added

- **Whole-frame probes work for Python.** `-probe file:line=*` is written to debugpy
  as `locals()`, so the frame comes back without naming a single expression — the
  same zero-configuration shape the CI step relies on, now for a second runtime.
  JavaScript still refuses it, by name: a logpoint evaluates while the program runs
  on, so there is no stopped frame to enumerate, and the language has no expression
  naming its own scope. Whether a runtime can do this is a property of that runtime,
  so it lives in its adapter profile, where it can be wrong for one language only.

## [0.3.0] - 2026-09-23

### Added

- **A probe can record the whole frame.** `-probe file:line=*`, or `"record": ["*"]`,
  captures every argument and local at the hit instead of expressions named in
  advance. Delve loads the frame itself and sends it with the hit, so this still
  costs no round trip per iteration. It exists for the case where nothing is known
  yet: a test failed in CI, the output names a line, and nobody is there to say
  which variables matter. Naming them is better when you can. Adapters that can only
  evaluate expressions refuse it by name rather than evaluating something called `*`.
- **`ci/debug-failing-test.sh` and [docs/CI.md](docs/CI.md)** — a pipeline step that
  re-runs the first failing test under the debugger and leaves a report, configuring
  nothing: the test's name, its package and the failing line are all in `go test`
  output already. Ready snippets for GitLab CI and GitHub Actions. It always exits
  zero, because a diagnostic that can break a pipeline stops being run.

- **`dbgmcp explain`** — follow one value from a command line. The tool had been available to an
  agent since the start and to a pipeline not at all, so this server advertised three questions it
  answers in one call and let a script reach two of them. The report leads with the last change and
  states whether the history is complete, because one cut short by a budget or a timeout can be
  missing the write that explains everything.

### Fixed

- **A refused watchpoint now names the reason that applies.** Every refusal drew the same advice
  about the hardware limits, so a value too wide to watch — a string, a slice, a struct — was met
  with a note about having too many watchpoints, sending the reader to count watchpoints instead of
  looking at the type. Delve refuses for four distinct reasons and each now gets its own next step.
  Ordering the checks is load-bearing: one refusal contains another as a substring, and the broader
  one used to swallow it.

- **`value_froze` no longer reports a latch.** A flag that flips once and then never moves again
  satisfied the rule's "was it genuinely changing before" check by having nothing for it to examine:
  with the frozen run starting at the second reading, the comparison loop ran zero times. The
  reported detail then claimed the value "changed at every hit", which was false — it changed once.
  Two readings must now precede the freeze. A rule that reports a latch teaches a reader to skip the
  whole section, which costs more than the rule is worth.

## [0.2.0] - 2026-09-23

### Added

- **`diff_runs`** — run the same probes over two runs and report the first point where
  they stopped agreeing. Starts, traces and tears down both runs itself, so the whole
  comparison is one call. It separates a differing value from a value present in one run
  and absent in the other, from a differing number of hits, from a probe reached in only
  one run, because those send a reader to different places. This is what `findings`
  structurally cannot do: nothing in a single transcript says what a value should have
  been, and a second run does.
- **`dbgmcp diff`** — the same comparison as a command, for a pipeline with no
  conversation to hold. Writes a markdown or JSON report under the caller's own labels
  for the two runs. Exits zero even when the runs diverge, like `dbgmcp trace`.
- A contract test over this server's tool surface and over the tool names shared with
  the JetBrains debugger plugin, so adding or renaming a tool here cannot be silent.

### Notes

- `diff_runs` never compares execution unit ids between runs. A goroutine or thread id is
  assigned within a single run, so units are aligned by the order they reached the probe
  instead. With one unit that is exact; where several reached it, the reply reports the
  matching as by arrival rather than presenting it as settled.

## [0.1.0] - 2026-09-23

First release. Everything below is new.

### Runtimes

- **Go** through Delve's native RPC API, with no IDE.
- **Python** and **JavaScript/TypeScript** through the Debug Adapter Protocol, one
  implementation with a profile each. TypeScript rides the JavaScript profile with source
  maps on.
- Each reports a capability set it can actually honour. Only Go has watchpoints,
  per-unit hit counts, execution-unit ancestry and breakpoints by symbol.

### Debugging
- Launch modes `test`, `debug` and `exec`; `attach` takes control of a process
  that is already running and leaves it running on detach.
- Breakpoints by symbol or by file and line, with conditions and hit-count
  conditions. Watchpoints. Stepping. Expression evaluation. Variable mutation.
- Execution units (goroutines) with the reason each blocked one is waiting, and
  the chain of units that created a given one.
- The debuggee's stdout and stderr, with a cursor for tailing.
- Build tags, so a suite kept behind one can be debugged at all, and deterministic test
  order, so a breakpoint in a named test stays where it was put.

### Answering questions rather than exposing primitives

- `trace_execution` records expressions at any number of probes and returns the
  whole run in one call.
- `explain_value` follows one value and returns every change to it — what it was,
  what it became, where, and with what stack.
- `wait_for_pause` returns location, stack, variables, source and recent output
  together, so the common follow-up questions need no further calls.
- `list_debug_targets` finds the packages and test functions in a project by
  reading source, executing nothing.

### Without an agent

- `dbgmcp trace` records expressions while a target runs and writes a report, for a
  pipeline that has no conversation to hold.

### Honesty machinery

- `describe_backend` reports capabilities as data the agent can read, including
  the platform and the `dlv` actually in use.
- A conformance suite runs against every backend and fails the build when a
  declared capability cannot be demonstrated.
- The acceptance test asserts how many MCP calls it takes to find a planted bug,
  so a change that makes the tools harder to use fails rather than degrades.
