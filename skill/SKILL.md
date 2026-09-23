---
name: runtime-debugging
description: Use when a program produces a wrong value, hangs, panics or fails a test and reading the code has not explained why. Drives a real debugger through MCP tools - breakpoints, stepping, watchpoints and tracing - against the running process, in a JetBrains IDE or headless.
---

# Runtime debugging

Reading code tells you what it should do. A debugger tells you what it did. Reach for this
the moment a hypothesis needs a fact: which branch ran, what the value actually was, which
goroutine is stuck, how many times a loop went round.

The tool names below are shared between the headless server and our JetBrains plugin, so this
works the same whether or not an IDE is open. Where a name exists in only one of them, it is
because only one of them can do that thing -- ask `describe_backend` rather than assuming.

## Read the backend before planning

Call `describe_backend` first. Debuggers differ in ways that decide your whole approach:

| Capability | What it changes |
|---|---|
| `breakpoint_by_symbol` | break on `pkg.Func` instead of hunting a line number |
| `watchpoints` | ask "what changed this value" directly, rather than guessing where to look |
| `trace_mode` | whether tracing perturbs timing -- decisive when chasing a race |
| `eval_calls_functions` | whether `evaluate_expression` may call methods |
| `set_variable` | whether you can test a fix without editing code |
| `hit_counts` | whether "it failed on the 90th iteration" is answerable |

Plan against what it reports. Do not discover the limits by failing into them.

## The loop

```
list_debug_targets      what can be run here (headless)
                        or list_run_configurations (IDE)
start_debug_session     stops BEFORE the first instruction
set_breakpoint          set them all now, while nothing has run
resume_execution
wait_for_pause          returns location, stack, variables, source, recent output
evaluate_expression     test the hypothesis against live state
step_over / step_into / step_out
```

**Set breakpoints before the first resume.** `start_debug_session` deliberately leaves the
target stopped at its entry point. Resuming first means racing the thing you came to watch.

**`wait_for_pause` already answered your next three questions.** It returns the stack, the
variables in scope, the surrounding source and the debuggee's recent output. Calling
`get_variables`, `get_stack_trace` and `get_source_context` after it spends round trips on
data you already have. Use `get_debug_session_status` only to re-read a pause later.

**Stepping returns where it landed.** `step_over` and friends give you the new state directly;
no follow-up call.

## Spend one call, not a hundred

**Watching a loop:** do not stop on every iteration. Use `trace_execution` with the expressions
you want recorded. The debugger evaluates them at each hit and continues on its own, so a
thousand iterations cost one call.

```json
{"probes": [{"symbol": "billing.lineTotal", "record": ["it.Name", "it.Price * it.Qty"]}]}
```

Read `mode` and `perturbs_timing` in the reply. Unless `mode` is `buffered`, the program really
did stop at each hit -- the saving is round trips, not observer effect. For a race, that
distinction is the whole answer.

`probes_never_hit` tells you a probe never fired, which is a different problem from a probe that
fired and found nothing.

**Reaching one specific iteration:** a condition or a hit count, not a hundred resumes.

```json
{"symbol": "billing.lineTotal", "condition": "it.Price > 100"}
{"symbol": "worker.process", "hit_condition": "> 90"}
```

**"What changed this value":** if `watchpoints` is available, ask directly instead of guessing
where to put a breakpoint. The variable must already exist -- stopping at a function's entry is
before its locals are declared, so step past the declaration first. Watchpoints are a hardware
feature: about four at once, and each dies when its stack frame returns.

## Reading values

Variables come back as paths -- `it`, `it.Price`, `items[2].Name`. **The path is an expression**:
paste it straight into `evaluate_expression`, `set_variable` or a breakpoint condition.

`truncated: true` means a budget cut the value short, not that the data is missing. Raise
`max_depth`, `max_string_len` or `max_array_values` for that one call rather than for all of them.

## When it hangs

1. `pause_execution` -- stop it wherever it is
2. `list_execution_units` -- goroutines, threads or tasks, with their state
3. Look for units reported `blocked`, and read the reason they are waiting
4. `get_stack_trace` on those units
5. `get_unit_ancestors` for where a unit came from, if the backend supports it

A deadlock looks like several units blocked on each other. The wait reasons name the mechanism.

## Confirming a fix without editing code

`set_variable` changes a value in the running program, so you can push the state that triggers
the bug, or the value you believe is correct, and watch what happens. It reads the value back,
so you see what actually took effect. Preview with `evaluate_expression` first.

## What to be careful about

- **The binary is not the one that ships.** Debug builds disable the optimiser so variables
  survive. `optimisations_disabled` says so. An optimisation-dependent bug may not reproduce.
- **`evaluate_expression` may not call functions.** Several backends refuse by default, because
  calling into the target runs its code. Check `eval_calls_functions`; prefer reading fields.
- **Tracing recorded nothing** usually means the probe was placed after the code had already run
  past it, or on a branch never taken. Check `probes_never_hit` before concluding anything.
- **A session left running holds a process.** Call `stop_debug_session` when finished.
- **Read the program's own output.** `get_session_output` returns stdout and stderr. Outside an
  IDE there is no console, and a panic message is often the shortest path to the answer.

## Finding a wrong value: the short version

1. `set_breakpoint` on the function that produces it, conditioned on the case that goes wrong
2. `resume_execution`, then `wait_for_pause`
3. `evaluate_expression` on what the code computes, and on what it should compute
4. The disagreement is the bug, and the stop location is where it lives

That is four to six calls. If you find yourself on the twentieth, stop and use
`trace_execution` or a condition instead.
