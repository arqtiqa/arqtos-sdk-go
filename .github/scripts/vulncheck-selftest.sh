#!/usr/bin/env bash
# The vulnerability gate's own falsifiers (#153). A gate that has never failed may
# not be looking, so this proves both refusals: the first on a throwaway copy of the
# tree it pins to a vulnerable module, the second on this tree read-only with a
# scanner stub. The working copy is never written.
#
#   1. A tree pinned to a KNOWN-vulnerable module must FAIL the gate naming the
#      advisory, its alias and the module: golang.org/x/net v0.55.0 carries
#      GO-2026-5942 (CVE-2026-46600), fixed in v0.56.0, an entry the Go
#      vulnerability database holds. GHSA-vp52-pcj8-j9qc on grpc, the advisory
#      that motivated the gate, is NOT in that database as of 2026-09-02, so no
#      govulncheck gate can name it; Dependabot remains the instrument for it.
#   2. A scanner that prints nothing must FAIL the gate: an empty report is not
#      a clean report.
set -euo pipefail
GO="${GO:-go}"
root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# Tracked and untracked-but-not-ignored files alike, so the module the gate SCANS is
# the working tree's; the gate exercised is always this tree's, run from the root below.
mkdir -p "$tmp/tree" "$tmp/bin"
(cd "$root" && git ls-files -z --cached --others --exclude-standard | tar --null -T - -cf -) | tar -xf - -C "$tmp/tree"
(cd "$tmp/tree" && "$GO" get golang.org/x/net@v0.55.0 >/dev/null 2>&1)

# The gate is run FROM THE ROOT with the throwaway as its module argument, so the
# scanner is the one this tree pins even though the pin below drags the throwaway's
# own x/vuln requirement down with x/net.
status=0
out="$(cd "$root" && GO="$GO" bash .github/scripts/vulncheck.sh "$tmp/tree" 2>&1)" || status=$?
if [ "$status" -eq 0 ]; then
	echo "vulncheck-selftest: FAILED — the gate passed a tree pinned to golang.org/x/net v0.55.0; it is not looking" >&2
	printf '%s\n' "$out" >&2
	exit 1
fi
for want in GO-2026-5942 CVE-2026-46600 golang.org/x/net; do
	case "$out" in
	*"$want"*) ;;
	*)
		echo "vulncheck-selftest: FAILED — the gate refused the vulnerable pin (exit $status) without naming $want" >&2
		printf '%s\n' "$out" >&2
		exit 1
		;;
	esac
done

printf '#!/bin/sh\nexit 0\n' >"$tmp/bin/go"
chmod +x "$tmp/bin/go"
status=0
out="$(cd "$root" && GO="$tmp/bin/go" bash .github/scripts/vulncheck.sh 2>&1)" || status=$?
case "$status:$out" in
0:*)
	echo "vulncheck-selftest: FAILED — the gate passed a scanner that printed nothing" >&2
	exit 1
	;;
*"no report"*) ;;
*)
	echo "vulncheck-selftest: FAILED — the gate refused an empty report (exit $status) without saying so" >&2
	printf '%s\n' "$out" >&2
	exit 1
	;;
esac
echo "vulncheck-selftest: the gate fails a tree pinned to golang.org/x/net v0.55.0 naming GO-2026-5942 (CVE-2026-46600), and fails a scanner that printed nothing"
