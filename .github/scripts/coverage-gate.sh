#!/usr/bin/env bash
#
# Fails when hand-written code falls below a statement-coverage floor.
#
# Two package trees are removed from the denominator. Both are measurement
# corrections, not a lowered bar — leaving them in would make the number report
# something other than how well this module's own code is tested:
#
#   connectorpb/  protoc output. Regenerated wholesale from proto/, never edited
#                 by hand. A test over it tests protoc.
#   examples/     the reference provider is a `main`, and roundtrip_test.go
#                 drives it the way a host does: `go build` it, then launch it
#                 as a real go-plugin subprocess. In-process instrumentation
#                 cannot see a subprocess, so its statements always read 0%
#                 however thoroughly it is exercised.
#
# Usage: coverage-gate.sh <coverage profile>
#   MIN_COVERAGE  floor, percent (default 75, the arqtos-cli house standard)

set -euo pipefail

profile="${1:-}"
if [ -z "$profile" ]; then
	echo "usage: $0 <coverage profile>" >&2
	exit 2
fi
if [ ! -s "$profile" ]; then
	echo "coverage-gate: '$profile' is missing or empty — the test step produced no profile" >&2
	exit 1
fi

min="${MIN_COVERAGE:-75}"
filtered="$(mktemp)"
trap 'rm -f "$filtered"' EXIT

grep -v -e '/connectorpb/' -e '/examples/' "$profile" >"$filtered" || true
if [ ! -s "$filtered" ]; then
	echo "coverage-gate: nothing left after excluding generated and out-of-process packages" >&2
	exit 1
fi

pct="$(go tool cover -func="$filtered" | awk '/^total:/ {gsub("%",""); print $NF}')"
if [ -z "$pct" ]; then
	echo "coverage-gate: could not read a total from the profile" >&2
	exit 1
fi

echo "coverage-gate: ${pct}% of hand-written statements (floor ${min}%)"
awk -v p="$pct" -v m="$min" 'BEGIN { if (p+0 < m+0) { printf "coverage-gate: FAIL — %s%% is below the %s%% floor\n", p, m; exit 1 } }'
