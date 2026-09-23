#!/bin/sh
# Re-run the first failing Go test under the debugger and leave an artifact.
#
# A failing test in CI has no conversation to hold. It leaves a log, a stack and
# an assertion -- what it never leaves is the state the code was actually in. This
# runs the same test again under dbgmcp, records every argument and local at the
# line that failed, and writes a report a reviewer can open.
#
# Nothing has to be configured. The test name, its package and the failing line
# are all in `go test` output already; this reads them from there.
#
# Usage:   ci/debug-failing-test.sh [output-file]
# Exit:    always 0 -- the failing test fails the build, not the diagnostic.
#          A diagnostic that can break a pipeline stops being run.
set -eu

OUT="${1:-debug-report.md}"
LOG="$(mktemp)"
trap 'rm -f "$LOG"' EXIT

if go test ./... >"$LOG" 2>&1; then
    echo "All tests passed; nothing to debug." >&2
    exit 0
fi

# "--- FAIL: TestName (0.00s)", possibly indented for a subtest.
TEST=$(sed -n 's/^ *--- FAIL: \([^ ]*\).*/\1/p' "$LOG" | head -1)
# The first "file.go:123:" reported under it -- where the assertion gave up.
WHERE=$(sed -n 's/^ *\([A-Za-z0-9_.-]*\.go\):\([0-9]*\):.*/\1:\2/p' "$LOG" | head -1)
# "FAIL\tmodule/path/to/pkg\t0.2s"
PKG=$(sed -n 's/^FAIL[[:space:]]*\([^[:space:]]*\)[[:space:]].*/\1/p' "$LOG" | head -1)

if [ -z "$TEST" ] || [ -z "$WHERE" ] || [ -z "$PKG" ]; then
    echo "Tests failed, but not in a shape this can re-run (no test name, line or package found)." >&2
    sed -n '1,40p' "$LOG" >&2
    exit 0
fi

DIR=$(go list -f '{{.Dir}}' "$PKG" 2>/dev/null) || DIR=""
if [ -z "$DIR" ]; then
    echo "Could not locate the source of $PKG." >&2
    exit 0
fi

echo "Re-running $TEST under the debugger, recording the frame at $WHERE" >&2

# '*' records every argument and local at that line. Naming expressions is better
# when you know which ones matter; here nobody does yet.
dbgmcp trace \
    -dir "$DIR" -mode test -target . \
    -test "$TEST" \
    -probe "$WHERE=*" \
    -format md -out "$OUT" || {
        echo "The debugger could not re-run it; the test output above still stands." >&2
        exit 0
    }

echo "Wrote $OUT" >&2
