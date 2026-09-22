## What this changes

<!-- The behaviour, not the diff. -->

## How it was verified

<!--
Paste the command and its output. `go test ./...` passing is the floor, not the
evidence: say which test covers the new behaviour, and what it asserts.
-->

## Checklist

- [ ] `go test ./... -count=1` passes
- [ ] New behaviour is covered by a test that runs a real program, not a mock
- [ ] Any capability this declares is exercised by the conformance suite in both
      directions — it works when declared, and refuses by name when not
- [ ] The acceptance test's call count did not go up, or the increase is
      explained here
- [ ] New error messages name the cause and the next thing to try
