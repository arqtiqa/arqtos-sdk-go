#!/usr/bin/env bash
# The vulnerability gate (#153). govulncheck's text mode exits 3 on ANY finding,
# and at the pinned toolchain the standard library carries findings no dependency
# bump can fix, so a gate on that exit code would be red forever and read as noise.
# This reads the JSON stream instead and applies ONE rule, stated so a review can
# refuse it: a finding whose vulnerable frame is in a THIRD-PARTY module FAILS the
# gate, naming the advisory, its aliases, the module and the fixed version; a
# finding in the STANDARD LIBRARY is REPORTED with the toolchain bump as its remedy
# and does not fail the gate. The standard-library line is printed before any
# third-party refusal, so neither masks the other. The toolchain the findings are
# judged against is the one the run selected: go.mod's go directive is a floor,
# and pinning it is arqtiqa/arqtos-sdk-go#156, not this script.
#
# Usage: vulncheck.sh [module-dir]. The scanner is the one the go.mod in the
# current directory pins through its tool directive; module-dir (default .) is the
# module it scans, so the selftest can point it at a throwaway copy without that
# copy's go.mod choosing the scanner.
set -euo pipefail
GO="${GO:-go}"
target="${1:-.}"
report="$(mktemp)"
trap 'rm -f "$report"' EXIT

# JSON mode exits 0 whatever it finds; a scanner or database failure still exits
# non-zero and propagates through set -e.
"$GO" tool govulncheck -C "$target" -format json ./... >"$report"

# An empty report is not a clean report: the stream opens with the scanner's config.
if [ "$(grep -cF '"scanner_name": "govulncheck"' "$report")" -ne 1 ]; then
	echo "vulncheck: FAILING — govulncheck produced no report; nothing was scanned" >&2
	exit 1
fi
go_version="$(awk -F'"' '/^    "go_version": "/ {print $4; exit}' "$report")"

# One record per finding (advisory, module of the vulnerable frame, fixed version)
# and one per alias, read off govulncheck's protocol v1.0.0 indentation.
rows="$(awk '
	/^  "osv": \{/      { mode = "osv"; id = ""; inal = 0; next }
	/^  "finding": \{/  { mode = "finding"; fo = ""; fx = ""; fm = ""; intrace = 0; next }
	mode == "osv" && /^    "id": "/ { id = $0; sub(/.*"id": "/, "", id); sub(/".*/, "", id); next }
	mode == "osv" && /^    "aliases": \[/ { inal = 1; next }
	mode == "osv" && inal && /^    \]/ { inal = 0; next }
	mode == "osv" && inal && /^      "/ { a = $0; sub(/^ *"/, "", a); sub(/".*/, "", a); print "ALIAS\t" id "\t" a; next }
	mode == "finding" && /^    "osv": "/ { fo = $0; sub(/.*"osv": "/, "", fo); sub(/".*/, "", fo); next }
	mode == "finding" && /^    "fixed_version": "/ { fx = $0; sub(/.*"fixed_version": "/, "", fx); sub(/".*/, "", fx); next }
	mode == "finding" && /^    "trace": \[/ { intrace = 1; next }
	mode == "finding" && intrace && fm == "" && /^        "module": "/ {
		fm = $0; sub(/.*"module": "/, "", fm); sub(/".*/, "", fm)
		print "FINDING\t" fo "\t" fm "\t" fx; mode = ""; next
	}
' "$report")"

third="$(printf '%s\n' "$rows" | awk -F'\t' '$1 == "FINDING" && $3 != "stdlib" { print $2 "\t" $3 "\t" $4 }' | sort -u)"
std="$(printf '%s\n' "$rows" | awk -F'\t' '$1 == "FINDING" && $3 == "stdlib" { print $2 "\t" $4 }' | sort -u)"

std_count="$(printf '%s\n' "$std" | awk -F'\t' 'NF { print $1 }' | sort -u | wc -l | tr -d ' ')"
if [ "$std_count" -gt 0 ]; then
	newest="$(printf '%s\n' "$std" | awk -F'\t' 'NF && $2 != "" { print $2 }' | sed -E 's/^(go|v)//' | sort -t. -k1,1n -k2,2n -k3,3n | tail -1)"
	if [ -n "$newest" ]; then remedy="raise the toolchain to go$newest"; else remedy="no fix is published yet"; fi
	echo "vulncheck: $std_count standard-library finding(s) at $go_version, the toolchain this run selected (go.mod's go directive is a floor, not a pin; arqtiqa/arqtos-sdk-go#156) — remedy: $remedy; these do not fail this gate"
else
	echo "vulncheck: 0 standard-library finding(s) at $go_version, the toolchain this run selected"
fi

if [ -n "$third" ]; then
	echo "vulncheck: FAILING — a third-party module this module requires carries a known vulnerability:" >&2
	while IFS=$'\t' read -r id mod fixed; do
		aliases="$(printf '%s\n' "$rows" | awk -F'\t' -v id="$id" '$1 == "ALIAS" && $2 == id { print $3 }' | sort -u | paste -sd, - | sed 's/,/, /g')"
		echo "         $id (${aliases:-no alias}) in $mod, fixed in ${fixed:-no published fix}" >&2
	done <<<"$third"
	echo "         Bump the requirement past the fixed version; a Dependabot banner is not a gate." >&2
	exit 1
fi

echo "vulncheck: 0 third-party finding(s)"
