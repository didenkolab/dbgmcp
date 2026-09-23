# Changelog

All notable changes are recorded here. This project follows [semantic
versioning](https://semver.org).

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
