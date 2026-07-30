#!/usr/bin/env bash
#
# Fails when this module gains a direct or transitive dependency on
# arqtos-cli — or on any other arqtiqa module. This SDK is the public IP
# boundary the whole external-developer model rests on: an outside
# contributor builds against it and must never pull arqtos-cli, or anything
# else under github.com/arqtiqa/, into their build. Every arqtiqa module
# other than this one is presumed private until proven otherwise, so the
# check denies the whole org prefix rather than naming arqtos-cli alone —
# that is what "any private arqtiqa module" means in code.
#
# Two graphs are walked, because each catches what the other misses:
#
#   go mod graph          the REQUIRE graph: every module version in the
#                          build list, direct or indirect, whether or not any
#                          package actually imports it yet. Catches a bare
#                          `require` line before a single import statement
#                          exists to give it away.
#   go list -deps -test   the IMPORT graph: every package this module's own
#                          code — production AND test — actually reaches.
#                          -test matters: a private import that only a
#                          _test.go file uses never reaches an external
#                          consumer's build, but it does reach
#                          `go mod tidy` / `go mod download all` run inside
#                          THIS repo, which is what "still builds standalone
#                          with no arqtos-cli present" (this gate's own last
#                          acceptance criterion) depends on.
#
# A go.mod grep is exactly the check that stays green while the graph is
# dirty: the dependency this gate exists for arrives as a convenience import
# several modules down, promoted from indirect to direct by someone else's
# PR, long before it is ever spelled out at the top of this module's own
# go.mod. Neither `go mod graph` nor `go list -deps` alone is the full
# picture either — a module can be required without being imported (a dead
# `require` line, still a leak surface for `go mod download all`) or
# imported only from a test file (never in `go.mod`'s direct requires at
# all, but very much in the module's own build). Both graphs, together, are
# what "the full dependency graph" in the story means.
#
# Usage: scripts/check-ip-isolation.sh   (GO=... to override the toolchain)

set -uo pipefail

GO="${GO:-go}"
root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root" || exit 1

self_module="$("$GO" list -m 2>&1)"
if [ -z "$self_module" ]; then
	echo "check-ip-isolation: could not determine this module's own path" >&2
	exit 1
fi

# True (0) iff $1 is a bare module path (no @version) naming an arqtiqa-org
# module OTHER than this one.
is_foreign_arqtiqa() {
	case "$1" in
	github.com/arqtiqa/*)
		[ "$1" != "$self_module" ]
		;;
	*)
		false
		;;
	esac
}

# --- 1. The require graph -----------------------------------------------
graph_out="$("$GO" mod graph 2>&1)"
graph_status=$?
if [ "$graph_status" -ne 0 ]; then
	echo "check-ip-isolation: 'go mod graph' failed:" >&2
	echo "$graph_out" >&2
	exit 1
fi

graph_hits=""
while IFS= read -r line; do
	[ -z "$line" ] && continue
	for field in $line; do
		mod="${field%%@*}"
		if is_foreign_arqtiqa "$mod"; then
			graph_hits="${graph_hits}${mod}
"
		fi
	done
done <<<"$graph_out"

# --- 2. The import graph, including test-only imports -------------------
deps_out="$("$GO" list -deps -test -f '{{if .Module}}{{.Module.Path}}{{end}}' ./... 2>&1)"
deps_status=$?
if [ "$deps_status" -ne 0 ]; then
	echo "check-ip-isolation: 'go list -deps -test ./...' failed:" >&2
	echo "$deps_out" >&2
	exit 1
fi

import_hits=""
while IFS= read -r mod; do
	[ -z "$mod" ] && continue
	if is_foreign_arqtiqa "$mod"; then
		import_hits="${import_hits}${mod}
"
	fi
done <<<"$deps_out"

all_hits="$(printf '%s\n%s\n' "$graph_hits" "$import_hits" | sed '/^$/d' | sort -u)"

if [ -z "$all_hits" ]; then
	echo "check-ip-isolation: clean — no arqtiqa module other than ${self_module} in the require or import graph"
	exit 0
fi

echo "check-ip-isolation: IP ISOLATION VIOLATED" >&2
echo "${self_module} depends on the following arqtiqa module(s), directly or transitively:" >&2
echo >&2

while IFS= read -r mod; do
	[ -z "$mod" ] && continue
	echo "  - ${mod}" >&2

	if printf '%s\n' "$import_hits" | grep -qx "$mod"; then
		echo "    import chain (go mod why -m ${mod}):" >&2
		"$GO" mod why -m "$mod" 2>&1 | sed 's/^/      /' >&2
	fi

	parents="$(printf '%s\n' "$graph_out" | grep -F " ${mod}@" | awk '{print $1}' | sed 's/@.*$//' | sort -u)"
	if [ -n "$parents" ]; then
		echo "    required by (go mod graph):" >&2
		printf '%s\n' "$parents" | sed 's/^/      /' >&2
	fi
	echo >&2
done <<<"$all_hits"

echo "This module is the public IP boundary; it must never depend on arqtos-cli" >&2
echo "or any other arqtiqa module, directly or transitively. Remove the import" >&2
echo "chain named above." >&2

exit 1
