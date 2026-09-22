# Contributing

Bug reports and patches are welcome. A few things about this codebase are unusual
enough to be worth reading before you start.

## Run the tests

```bash
go install github.com/go-delve/delve/cmd/dlv@v1.27.2
go test ./... -count=1
```

The suite launches real processes under a real debugger. It takes about fifteen
seconds. The live tests skip, rather than fail, when Delve is absent.

**There are no mocked debuggers here, and please do not add one.** A mocked
debugger proves that the mock behaves as written; it proves nothing about whether
this can debug. Every claim in this repository is backed by a test that runs a
program.

## The capability rule

`describe_backend` reports what a backend can do, and an agent plans against it.
A capability that is declared but not exercised is worse than no capability model
at all, because the agent will trust it and be wrong in a way it cannot detect.

So: **if you declare a capability, the conformance suite must exercise it.**
`internal/backend/conformance` runs against every backend and checks both
directions — a declared capability must demonstrably work, and an undeclared one
must refuse with a message that names what is missing.

This is not ceremony. The first run of that suite caught a capability this server
was declaring and could not deliver, in the same commit that declared it.

## The round-trip budget

`internal/mcpserver/acceptance_test.go` drives only MCP tools, the way an agent
would, and asserts how many calls it takes to find a planted bug. The sequence is
written as the minimum an agent would actually need — neither padded nor golfed.

If your change makes that number go up, the test fails. That is deliberate: a
change that makes the tools harder to use should be justified in the change that
caused it, not absorbed quietly.

## Error messages are a feature

The message is the only failure signal an agent gets, and an agent that is told
the cause and the next thing to try will recover, while one that gets "operation
failed" will retry the same call. Prefer:

> A watchpoint needs the variable to exist already: stopping at a function's
> entry is before its locals are declared. Step past the declaration, then set
> the watchpoint.

over `could not find symbol value`. Several existing messages exist precisely
because the terse version sent a debugging session in circles.

## Adding a backend

The interface is `internal/backend.Backend`, and the neutral model it speaks is
`internal/model`. Nothing above the backend layer may import Delve, DAP or any
other protocol.

Write the conformance fixture first. If a capability does not hold, declare it
`none` — an honest `none` is worth more than an aspirational `full`.

## Style

Comments explain **why**, not what. If a line needs a comment to say what it
does, the line is the problem. The existing code leans on this heavily; please
match it rather than working around it.
