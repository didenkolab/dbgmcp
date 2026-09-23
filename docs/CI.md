# Wiring it into a pipeline

A failing test in CI has no conversation to hold. It leaves a log, a stack and an
assertion — what it never leaves is the state the code was actually in. The point
of this page is to make it leave that too, without anyone configuring anything.

## The zero-configuration step

`ci/debug-failing-test.sh` re-runs the first failing test under the debugger and
writes a report. Everything it needs — the test's name, its package, the line the
assertion gave up on — is already in `go test` output, so it reads it from there.

It always exits zero. The failing test fails the build; the diagnostic must not,
because a diagnostic that can break a pipeline stops being run.

### GitLab CI

```yaml
test:
  image: golang:1.26
  script:
    - go test ./...
  after_script:
    - go install github.com/go-delve/delve/cmd/dlv@v1.27.2
    - go install github.com/didenkolab/dbgmcp/cmd/dbgmcp@latest
    - ci/debug-failing-test.sh debug-report.md || true
  artifacts:
    when: on_failure
    paths: [debug-report.md]
    expire_in: 1 week
```

`after_script` runs whether the job passed or failed, and the script says so and
stops when nothing failed. Installing the two binaries there rather than in the
image keeps a green pipeline as fast as it was.

### GitHub Actions

```yaml
      - name: Test
        run: go test ./...

      - name: Debug the failure
        if: failure()
        run: |
          go install github.com/go-delve/delve/cmd/dlv@v1.27.2
          go install github.com/didenkolab/dbgmcp/cmd/dbgmcp@latest
          ci/debug-failing-test.sh debug-report.md

      - uses: actions/upload-artifact@v4
        if: failure()
        with:
          name: debug-report
          path: debug-report.md
```

## What the automatic report contains, and what it does not

The probe goes on the line the assertion failed at, and records **every argument
and local in that frame** — that is what `*` means in `-probe file:line=*`.

So the automatic artifact shows the value the test observed and the inputs it
built to get there. That is the honest limit: it is the *test's* frame. The
function that computed the wrong answer is one level down, and nothing in the test
output says which function that is, so the script does not guess.

It also means the test's own machinery — `*testing.T` and friends — appears in the
table. Noise, but truthful noise: a filter would have to decide what is
uninteresting, and that decision belongs to whoever knows the code.

### One line makes it sharper

When you do know which function is wrong, name it and probe there instead:

```bash
dbgmcp trace -dir ./internal/pricing -mode test -target . \
  -test TestLoyalCustomer \
  -probe 'pricing.FinalPrice=*' \
  -format md -out debug-report.md
```

Same `*`, but now the frame is the one doing the arithmetic: its arguments, its
locals, and what it was about to return.

## When one input works and another does not

The sharper question, and the one no single run can answer: nothing in one
transcript says what a value *should* have been. A passing run does.

```bash
dbgmcp diff -dir ./internal/pricing -target . \
  -test-a TestOrdinaryCustomer -label-a passing \
  -test-b TestLoyalCustomer    -label-b failing \
  -probe 'pricing.FinalPrice=*' \
  -format md -out divergence.md
```

The report names the earliest point the two stopped agreeing, and says whether the
difference is a value, an absence, a different number of passes, or a branch only
one run reached.

## Build tags

A suite kept behind a tag cannot be built without it, so it cannot be debugged
either — and that is usually the half that talks to a database:

```bash
dbgmcp trace -tags "integration" -test TestCharge -probe 'billing.Charge=*'
```

## Before you trust a report

- **`perturbs_timing`** is true in every report this produces. The debuggee stopped
  at each hit. The saving is round trips, not observer effect, so a difference that
  depends on timing may be an effect of the measurement.
- **`probes_never_hit`** means the line was never reached. An empty transcript
  there is a misplaced probe, not absent data.
- **Findings are observations, not verdicts.** They notice an anomalous shape — a
  trend that reversed, a value that froze — and carry the readings they rest on.
  None of them can tell a wrong number from a right one.
