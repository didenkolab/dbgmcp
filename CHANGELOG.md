# Changelog

All notable changes are recorded here. This project follows [semantic
versioning](https://semver.org).

## [Unreleased]

First public release. Everything below is new.

### Debugging

- Go programs debugged headlessly through Delve's native RPC API, with no IDE.
- Python through the Debug Adapter Protocol and debugpy, behind the same tools. The
  backend reports a genuinely smaller capability set rather than pretending parity.
- Launch modes `test`, `debug` and `exec`; `attach` takes control of a process
  that is already running and leaves it running on detach.
- Breakpoints by symbol or by file and line, with conditions and hit-count
  conditions. Watchpoints. Stepping. Expression evaluation. Variable mutation.
- Execution units (goroutines) with the reason each blocked one is waiting, and
  the chain of units that created a given one.
- The debuggee's stdout and stderr, with a cursor for tailing.

### Answering questions rather than exposing primitives

- `trace_execution` records expressions at any number of probes and returns the
  whole run in one call.
- `explain_value` follows one value and returns every change to it — what it was,
  what it became, where, and with what stack.
- `wait_for_pause` returns location, stack, variables, source and recent output
  together, so the common follow-up questions need no further calls.
- `list_debug_targets` finds the packages and test functions in a project by
  reading source, executing nothing.

### Honesty machinery

- `describe_backend` reports capabilities as data the agent can read, including
  the platform and the `dlv` actually in use.
- A conformance suite runs against every backend and fails the build when a
  declared capability cannot be demonstrated.
- The acceptance test asserts how many MCP calls it takes to find a planted bug,
  so a change that makes the tools harder to use fails rather than degrades.
