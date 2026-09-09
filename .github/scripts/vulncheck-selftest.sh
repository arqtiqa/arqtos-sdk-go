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
#   3. A report scanned at a toolchain other than go.mod's pin must FAIL the gate
#      naming both, so a scan the pin did not govern is never a verdict (#156).
#   4. A pin that lags the newest standard-library fix past the ceiling must FAIL
#      the gate naming the pin and the fix; one patch behind passes, two fail, so
#      the ceiling is pinned at one; a pin ahead of the fix passes; a fix only in
#      a newer minor fails whatever the ceiling; a finding with no published fix
#      is reported and fails nothing; when a lag and a requirement finding are
#      both due, both refusals print. Fixed versions are fed in the JSON stream's
#      shape (v1.26.6), the one the gate meets on a real report.
#   5. A finding in a pseudo-module that is no requirement (the toolchain) or in
#      this module itself must be classed with the standard library, never as a
#      requirement to bump, and its semver measures no toolchain lag; a module that
#      REPLACES a requirement counts as that requirement.
#   6. A tree whose go.mod carries no toolchain line must be refused by make.
#   7. make's gofmt must come from the pinned toolchain, not from the shell's: export
#      reaches recipes and not $(shell), so the pin is passed into that shell by hand.
# Cases 3 to 5 feed the gate a report through a scanner stub, the technique of
# case 2, so the gate under test is byte-identical and nothing is downloaded.
# Run through make: case 1 scans a real copy of this tree, and the gate refuses
# a scan at any toolchain but go.mod's pin, which make exports as GOTOOLCHAIN.
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
# A stub scanner answering the two calls the gate makes: the report for govulncheck,
# a requirement list for go list (one entry replaced). The report is govulncheck's
# protocol v1.0.0 shape, fixed versions as the JSON stream states them (v1.26.6 for
# a standard-library fix, never go1.26.6); a fourth and fifth argument add a finding.
stub() { # go_version fixed module [fixed2 module2]
	cat >"$tmp/report.json" <<EOF
{
  "config": {
    "protocol_version": "v1.0.0",
    "scanner_name": "govulncheck",
    "scanner_version": "v1.7.0",
    "db": "https://vuln.go.dev",
    "go_version": "$1"
  }
}
{
  "osv": {
    "schema_version": "1.3.1",
    "id": "GO-2026-0001",
    "aliases": [
      "CVE-2026-0001"
    ]
  }
}
{
  "finding": {
    "osv": "GO-2026-0001",
    "fixed_version": "$2",
    "trace": [
      {
        "module": "$3",
        "version": "$1",
        "package": "net/http",
        "function": "Serve"
      }
    ]
  }
}
EOF
	if [ -n "${4:-}" ]; then
		cat >>"$tmp/report.json" <<EOF
{
  "osv": {
    "schema_version": "1.3.1",
    "id": "GO-2026-0002",
    "aliases": []
  }
}
{
  "finding": {
    "osv": "GO-2026-0002",
    "fixed_version": "$4",
    "trace": [
      {
        "module": "$5",
        "version": "v0.55.0",
        "package": "$5/html",
        "function": "Parse"
      }
    ]
  }
}
EOF
	fi
	printf '#!/bin/sh\ncase "$*" in\n*"list -m all"*) printf "github.com/arqtiqa/arqtos-sdk-go\\\\ngolang.org/x/net v0.56.0\\\\ngolang.org/x/text v0.38.0 => example.com/textfork v0.1.0\\\\n" ;;\n*govulncheck*) cat "%s" ;;\n*) exit 0 ;;\nesac\n' "$tmp/report.json" >"$tmp/bin/go"
	chmod +x "$tmp/bin/go"
}
# A throwaway module directory whose go.mod pins the toolchain given.
pinned() { mkdir -p "$tmp/$1"; printf 'module example.com/throwaway\n\ngo 1.26\n\ntoolchain %s\n' "$2" >"$tmp/$1/go.mod"; }

pinned p260 go1.26.0
stub go1.26.8 v1.26.6 stdlib
status=0
out="$(cd "$root" && GO="$tmp/bin/go" bash .github/scripts/vulncheck.sh "$tmp/p260" 2>&1)" || status=$?
case "$status:$out" in
0:*) echo "vulncheck-selftest: FAILED — the gate passed a scan at go1.26.8 of a module pinned to go1.26.0" >&2; exit 1 ;;
*go1.26.8*go1.26.0*|*go1.26.0*go1.26.8*) ;;
*) echo "vulncheck-selftest: FAILED — the gate refused a scan the pin did not govern (exit $status) without naming both versions" >&2; printf '%s\n' "$out" >&2; exit 1 ;;
esac

stub go1.26.0 v1.26.6 stdlib
status=0
out="$(cd "$root" && GO="$tmp/bin/go" bash .github/scripts/vulncheck.sh "$tmp/p260" 2>&1)" || status=$?
case "$status:$out" in
0:*) echo "vulncheck-selftest: FAILED — the gate passed a pin six patch releases behind the newest standard-library fix" >&2; exit 1 ;;
*go1.26.0*go1.26.6*) ;;
*) echo "vulncheck-selftest: FAILED — the gate refused the lagging pin (exit $status) without naming the pin and the fix" >&2; printf '%s\n' "$out" >&2; exit 1 ;;
esac
pinned p265 go1.26.5
stub go1.26.5 v1.26.6 stdlib
if ! out="$(cd "$root" && GO="$tmp/bin/go" bash .github/scripts/vulncheck.sh "$tmp/p265" 2>&1)"; then
	echo "vulncheck-selftest: FAILED — the gate refused a pin one patch release behind the newest fix, inside the ceiling" >&2
	printf '%s\n' "$out" >&2
	exit 1
fi
pinned p268 go1.26.8
stub go1.26.8 v1.27.1 stdlib
status=0
out="$(cd "$root" && GO="$tmp/bin/go" bash .github/scripts/vulncheck.sh "$tmp/p268" 2>&1)" || status=$?
case "$status:$out" in
0:*) echo "vulncheck-selftest: FAILED — the gate passed a pin whose newest fix is only in a newer minor" >&2; exit 1 ;;
*go1.26.8*go1.27.1*minor*) ;;
*) echo "vulncheck-selftest: FAILED — the gate refused the newer-minor fix (exit $status) without naming the pin, the fix and the minor" >&2; printf '%s\n' "$out" >&2; exit 1 ;;
esac
stub go1.26.8 v1.26.6 stdlib
if ! out="$(cd "$root" && GO="$tmp/bin/go" bash .github/scripts/vulncheck.sh "$tmp/p268" 2>&1)"; then
	echo "vulncheck-selftest: FAILED — the gate refused a pin AHEAD of the newest fix" >&2
	printf '%s\n' "$out" >&2
	exit 1
fi
case "$out" in *"lags it by 0 patch"*) ;; *) echo "vulncheck-selftest: FAILED — a pin ahead of the fix did not read as a lag of 0" >&2; printf '%s\n' "$out" >&2; exit 1 ;; esac
stub go1.26.8 "" stdlib
if ! out="$(cd "$root" && GO="$tmp/bin/go" bash .github/scripts/vulncheck.sh "$tmp/p268" 2>&1)"; then
	echo "vulncheck-selftest: FAILED — the gate refused a finding with no published fix" >&2
	printf '%s\n' "$out" >&2
	exit 1
fi
case "$out" in *"no standard-library fix is published yet"*) ;; *) echo "vulncheck-selftest: FAILED — a finding with no fix was not reported as such" >&2; printf '%s\n' "$out" >&2; exit 1 ;; esac

for member in toolchain github.com/arqtiqa/arqtos-sdk-go; do
	stub go1.26.0 v1.26.1 "$member"
	status=0
	out="$(cd "$root" && GO="$tmp/bin/go" bash .github/scripts/vulncheck.sh "$tmp/p260" 2>&1)" || status=$?
	case "$out" in
	*"Bump the requirement"*|*"module this module requires"*)
		echo "vulncheck-selftest: FAILED — a finding in $member, which is no requirement, was prescribed a requirement bump" >&2
		printf '%s\n' "$out" >&2
		exit 1
		;;
	esac
	case "$out" in
	*"toolchain-class finding(s) at go1.26.0, the pinned toolchain, in $member;"*) ;;
	*) echo "vulncheck-selftest: FAILED — a finding in $member was not reported toolchain-class by name (exit $status)" >&2; printf '%s\n' "$out" >&2; exit 1 ;;
	esac
done
stub go1.26.8 v2.0.0 toolchain
if ! out="$(cd "$root" && GO="$tmp/bin/go" bash .github/scripts/vulncheck.sh "$tmp/p268" 2>&1)"; then
	echo "vulncheck-selftest: FAILED — a pseudo-module's semver was read as a Go version and failed the pin" >&2
	printf '%s\n' "$out" >&2
	exit 1
fi
case "$out" in *"no standard-library fix is published yet"*) ;; *) echo "vulncheck-selftest: FAILED — a pseudo-module's fix was measured as a toolchain lag" >&2; printf '%s\n' "$out" >&2; exit 1 ;; esac

pin="$(awk '/^toolchain /{print $2; exit}' "$root/go.mod")"
want="$(GOTOOLCHAIN="$pin" "$GO" env GOROOT)/bin/gofmt"
# GOTOOLCHAIN is cleared for this make, as a developer's shell has it: under a
# recipe the exported pin would reach the child make's shell and hide the defect.
got="$(cd "$root" && env -u GOTOOLCHAIN make -n fmt-check 2>/dev/null | sed -nE 's#.*[ (](/[^ ()]*gofmt).*#\1#p' | head -1)"
if [ "$got" != "$want" ]; then
	echo "vulncheck-selftest: FAILED — make's gofmt is $got, not the pinned toolchain's $want" >&2
	exit 1
fi

stub go1.26.8 v1.26.10 stdlib
status=0
out="$(cd "$root" && GO="$tmp/bin/go" bash .github/scripts/vulncheck.sh "$tmp/p268" 2>&1)" || status=$?
case "$status:$out" in
0:*) echo "vulncheck-selftest: FAILED — the gate passed a pin two patch releases behind the newest fix; the ceiling is one" >&2; exit 1 ;;
*"2 patch release(s) behind against a ceiling of 1"*) ;;
*) echo "vulncheck-selftest: FAILED — the gate refused the two-release lag (exit $status) without stating the lag and the ceiling" >&2; printf '%s\n' "$out" >&2; exit 1 ;;
esac
stub go1.26.8 v1.26.10 stdlib v0.56.0 golang.org/x/net
status=0
out="$(cd "$root" && GO="$tmp/bin/go" bash .github/scripts/vulncheck.sh "$tmp/p268" 2>&1)" || status=$?
case "$status:$out" in
0:*) echo "vulncheck-selftest: FAILED — the gate passed a report carrying both a lagging pin and a requirement finding" >&2; exit 1 ;;
*"2 patch release(s) behind"*"GO-2026-0002"*"golang.org/x/net"*) ;;
*) echo "vulncheck-selftest: FAILED — with both refusals due the gate printed one (exit $status): the toolchain-class line must come first and the requirement refusal must still name its finding" >&2; printf '%s\n' "$out" >&2; exit 1 ;;
esac
stub go1.26.8 v0.2.0 example.com/textfork
status=0
out="$(cd "$root" && GO="$tmp/bin/go" bash .github/scripts/vulncheck.sh "$tmp/p268" 2>&1)" || status=$?
case "$status:$out" in
0:*) echo "vulncheck-selftest: FAILED — a finding in a module that REPLACES a requirement passed as toolchain-class" >&2; exit 1 ;;
*"GO-2026-0001"*"example.com/textfork"*"Bump the requirement"*) ;;
*) echo "vulncheck-selftest: FAILED — the replaced requirement's finding was refused (exit $status) without naming it as a requirement" >&2; printf '%s\n' "$out" >&2; exit 1 ;;
esac

mkdir -p "$tmp/nopin"
printf 'module example.com/nopin\n\ngo 1.26\n' >"$tmp/nopin/go.mod"
cp "$root/Makefile" "$tmp/nopin/Makefile"
if (cd "$tmp/nopin" && make -n vulncheck >/dev/null 2>&1); then
	echo "vulncheck-selftest: FAILED — make accepted a go.mod with no toolchain line" >&2
	exit 1
fi
echo "vulncheck-selftest: the gate fails a tree pinned to golang.org/x/net v0.55.0 naming GO-2026-5942 (CVE-2026-46600), fails a scanner that printed nothing, refuses a scan the pin did not govern and a pin past the ceiling, passes a pin ahead of the fix and a finding with no fix, fails a lag of two against the ceiling of one and prints both refusals when both are due, classes a toolchain or main-module finding with the standard library and measures no lag over a pseudo-module's semver, counts a replacement among the requirements, and make refuses a go.mod with no toolchain line and runs the pinned toolchain's gofmt"
