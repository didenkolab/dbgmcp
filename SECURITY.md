# Security

## What this tool can do

`dbgmcp` gives an AI agent a debugger. That means it can, by design:

- run programs and tests on the machine it is installed on
- **attach to and suspend processes it did not start**, when given a pid
- read any value in a debuggee's memory that the debugger can reach
- change values in a running program
- evaluate expressions in the debuggee's context

Treat it as you would a shell. Do not point it at production, and do not run it
with more privilege than debugging requires.

### Attaching is the sharp edge

An attached process is suspended for as long as the session holds it. Everything
that process serves is stopped meanwhile. The session leaves it running on
detach — that is asserted by a test — but a forgotten session is an outage.

### Evaluation

Delve does not call functions in the target by default, which removes the worst
of the risk rather than defending against it: an expression cannot currently run
arbitrary code in the debuggee. This is a property of the backend, not a guard
this server implements. It is reported as `eval_calls_functions: guarded` by
`describe_backend` and checked by the conformance suite. If you add a backend
that does call functions, it needs a guard of its own.

## Reporting a vulnerability

Open a [security advisory](../../security/advisories/new) rather than a public
issue. Please include what an attacker would have to control, and what they gain.
