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
#   MIN_TESTS          minimum number of passing tests overall (default 500)
#   REQUIRED_PACKAGES  space-separated package suffixes that must each have
#                      contributed at least one passing test
#
# The required set is every package carrying a gate the module would otherwise
# ship unguarded:
#
#   mcpconform  the MCP protocol conformance gate
#   credential  the Resolution / BatchResult / fault types — where "a
#               credential that did not resolve cannot be read as an empty
#               one" is actually enforced
#   credconform the CredentialLoader conformance harness, including the
#               checks that catch a connector resolving everything to empty
#   transport   the wire presence rules for BOTH classes; the one place a
#               foreign provider's bytes become a host-side credential, and
#               where an unresolved directory read is kept from arriving as an
#               empty directory
#   plugin      the out-of-process (Track-B) provider stubs — the wire-to-host
#               boundary these obligations exist for, and where the
#               empty-vs-unresolved, batch-shape, suspended-principal,
#               membership-correspondence and min_host_version regression tests
#               actually live. A run that strips this package still resolves,
#               still batches and still reads a directory on the Go-native
#               (Track-A) path, so nothing else in this list notices it is
#               gone.
#   roster      the Resolution[T] / fault types — where "an unresolved roster
#               is not a roster of nobody" is actually enforced, and the one
#               whose failure mode revokes an entire estate's access at once
#   rosterconform the Roster conformance harness, including the four-case
#               truth tables that prove its declared-is-implemented checks are
#               not reading one signal twice, the check that refuses a
#               connector answering every list with EmptyRoster, and the
#               out-of-process run plus the demonstration that a connector
#               passing in-process can still fail across the wire
#   githubratelimit  the three-mechanism rate-limit gate — where "primary,
#               secondary and GraphQL point cost are three different things"
#               is actually enforced, where a rate-limited call is proven to
#               discard a partial value rather than return it, and where the
#               package's own tests are gated against asserting wall-clock
#               time. Two of those are silent when they regress: a collapsed
#               mechanism under-waits and looks like a flaky backend, and a
#               partial value that escapes looks like a complete answer.
#
# These were not required before, and the omission was exactly the failure the
# script exists to catch: the packages where this module's guarantees live
# could all have stopped contributing a single passing test with the gate
# still green. plugin in particular was stripped from a prior revision of
# this script's own required set — 28 passing tests, every one of them
# proving the Track-B blocker this module exists to close, removed with the
# gate staying green throughout, because MIN_TESTS never dropped far enough
# to notice and no required package named it.
#
# The floor is kept within reach of the real count (550 at the time of
# writing) rather than far below it. A floor far below would let most of the
# suite stop building before this gate noticed — which is the same "green
# because nothing looked" failure the script exists to prevent, and it is
# exactly how the plugin package was stripped unnoticed under an earlier floor.
# At 500, losing a large enough guard-bearing package still trips MIN_TESTS on
# its own, before REQUIRED_PACKAGES even names it — githubratelimit (118),
# rosterconform (81) and plugin (53) all do, at the current total. roster (38)
# does not, by itself: the margin between the real count and the floor is 50.
# That is exactly why REQUIRED_PACKAGES names every guard-bearing package
# explicitly rather than relying on MIN_TESTS alone — a package smaller than
# the current margin needs the explicit listing to be guarded at all, and which
# packages clear that bar shifts, package by package, as the suite grows.
#
# The margin also absorbs the tests that SKIP rather than fail on a runner
# without a usable subprocess: examples/credentialloader-provider (1) and
# examples/roster-provider (3). Neither example is in REQUIRED_PACKAGES for
# that reason — requiring a package whose tests legitimately skip would fail
# the gate on an environment rather than on the code. What guards the Track-B
# wire on every runner is plugin, whose gRPC round-trips are in-process and
# therefore always run.
#
# Raising this floor is part of adding a connector class OR a transport for
# one — or any package carrying a guarantee, which is how githubratelimit
# joined the list — not an afterthought: a floor left at the previous count is
# a floor the new work's entire test suite can vanish beneath.

set -euo pipefail

report="${1:-}"
if [ -z "$report" ]; then
	echo "usage: $0 <go test -json report>" >&2
	exit 2
fi

min_tests="${MIN_TESTS:-500}"
required_packages="${REQUIRED_PACKAGES:-mcpconform credential credconform transport plugin roster rosterconform githubratelimit}"

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
		echo "That package carries a conformance gate; a run without it is not a gate." >&2
		exit 1
	fi
done

echo "assert-tests-ran: ${passed} passing test(s); required packages exercised: ${required_packages}"
