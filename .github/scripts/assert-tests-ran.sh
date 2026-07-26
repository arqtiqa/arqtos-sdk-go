#!/usr/bin/env bash
#
# Fails when the test step reported success without actually running tests.
#
# A CI gate that passes because it executed nothing is worse than no gate: it
# reports green while the thing it guards is unguarded. This script reads the
# `go test -json` stream the test step recorded and refuses a run that is
# vacuous — too few tests overall, or none at all in a package that must
# always be exercised.
#
# Usage: assert-tests-ran.sh <report.json>
#   MIN_TESTS          minimum number of passing tests overall (default 40)
#   REQUIRED_PACKAGES  space-separated package suffixes that must each have
#                      contributed at least one passing test (default mcpconform)

set -euo pipefail

report="${1:-}"
if [ -z "$report" ]; then
	echo "usage: $0 <go test -json report>" >&2
	exit 2
fi

min_tests="${MIN_TESTS:-40}"
required_packages="${REQUIRED_PACKAGES:-mcpconform}"

if [ ! -s "$report" ]; then
	echo "assert-tests-ran: '$report' is missing or empty — the test step produced no machine-readable output" >&2
	exit 1
fi

passed="$(grep -c '"Action":"pass","Package":"[^"]*","Test":"' "$report" || true)"
if [ "$passed" -lt "$min_tests" ]; then
	echo "assert-tests-ran: only ${passed} test(s) passed, expected at least ${min_tests}." >&2
	echo "Either tests were skipped/not built, or the floor in this script is stale." >&2
	exit 1
fi

for pkg in $required_packages; do
	if ! grep -q "\"Action\":\"pass\",\"Package\":\"[^\"]*/${pkg}\",\"Test\":\"" "$report"; then
		echo "assert-tests-ran: no passing test recorded for package .../${pkg}" >&2
		echo "That package carries the protocol conformance gate; a run without it is not a gate." >&2
		exit 1
	fi
done

echo "assert-tests-ran: ${passed} passing test(s); required packages exercised: ${required_packages}"
