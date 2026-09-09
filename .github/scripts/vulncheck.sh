#!/usr/bin/env bash
# The vulnerability gate (#153, #156). govulncheck's text mode exits 3 on ANY
# finding, and the standard library carries findings no dependency bump can fix,
# so a gate on that exit code would be red forever and read as noise. This reads
# the JSON stream and applies rules stated so a review can refuse them:
#   - the scan must run at go.mod's pinned toolchain, its toolchain line, which
#     make exports as GOTOOLCHAIN; a scan at any other version is refused, since
#     a verdict the pin did not govern is a verdict about another machine;
#   - a finding whose vulnerable frame is in a module this module REQUIRES fails
#     the gate, naming the advisory, its aliases, the module and the fixed version;
#   - a finding anywhere else, the standard library or a pseudo-module such as
#     the toolchain that no go.mod requires, is toolchain-class: reported with
#     the toolchain bump as its remedy. The lag is measured over the rows whose
#     module is stdlib, by MODULE and never by the shape of the version: the JSON
#     stream states a standard-library fix as v1.26.6 where text mode says
#     go1.26.6, and a pseudo-module's fixed version is a semver that says nothing
#     about a toolchain lag whatever it looks like. The gate fails when the pin
#     lags the newest standard-library fix by more than CEILING patch releases
#     of its minor, or when that fix exists only in a newer minor. One patch
#     release is one Go security cycle; zero would redden the gate the day a
#     fix ships.
# The toolchain-class line prints before any requirement refusal, so neither masks
# the other.
#
# Usage: vulncheck.sh [module-dir]. The scanner is the one the go.mod in the
# current directory pins through its tool directive; module-dir (default .) is the
# module it scans and whose go.mod carries the pin judged.
set -euo pipefail
GO="${GO:-go}"
target="${1:-.}"
CEILING=1
report="$(mktemp)"
trap 'rm -f "$report"' EXIT

pin="$(awk '/^toolchain /{print $2; exit}' "$target/go.mod")"
if [ -z "$pin" ]; then
	echo "vulncheck: FAILING — $target/go.mod carries no toolchain line, so there is no pin to judge against" >&2
	exit 1
fi

# JSON mode exits 0 whatever it finds; a scanner or database failure still exits
# non-zero and propagates through set -e.
"$GO" tool govulncheck -C "$target" -format json ./... >"$report"

# An empty report is not a clean report: the stream opens with the scanner's config.
if [ "$(grep -cF '"scanner_name": "govulncheck"' "$report")" -ne 1 ]; then
	echo "vulncheck: FAILING — govulncheck produced no report; nothing was scanned" >&2
	exit 1
fi
go_version="$(awk -F'"' '/^    "go_version": "/ {print $4; exit}' "$report")"
if [ "$go_version" != "$pin" ]; then
	echo "vulncheck: FAILING — the scan ran at $go_version while $target/go.mod pins $pin; a verdict the pin did not govern is no verdict (run it through make, which exports the pin)" >&2
	exit 1
fi

# The requirement set is what the module's go.mod resolves, the main module excluded
# and a replaced module counted under both its required path and its replacement,
# since the scanner names the replacement; joined on a character no module path
# carries, and a finding's module is judged against it, never against one name.
requires="$("$GO" -C "$target" list -m all | awk 'NR > 1 { print $1; if ($3 == "=>") print $4 }' | paste -sd';' -)"

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

classify='BEGIN { n = split(req, a, ";"); for (i = 1; i <= n; i++) if (a[i] != "") r[a[i]] = 1 }'
third="$(printf '%s\n' "$rows" | awk -F'\t' -v req="$requires" "$classify"' $1 == "FINDING" && ($3 in r) { print $2 "\t" $3 "\t" $4 }' | sort -u)"
tool="$(printf '%s\n' "$rows" | awk -F'\t' -v req="$requires" "$classify"' $1 == "FINDING" && !($3 in r) { print $2 "\t" $3 "\t" $4 }' | sort -u)"

status=0
tool_count="$(printf '%s\n' "$tool" | awk -F'\t' 'NF { print $1 }' | sort -u | wc -l | tr -d ' ')"
if [ "$tool_count" -gt 0 ]; then
	modules="$(printf '%s\n' "$tool" | awk -F'\t' 'NF { print $2 }' | sort -u | paste -sd, - | sed 's/,/, /g')"
	# The lag is over the stdlib rows by module; the version prefix is whatever the
	# producer wrote (v in the JSON stream, go in text mode) and is not the rule.
	newest="$(printf '%s\n' "$tool" | awk -F'\t' 'NF && $2 == "stdlib" && $3 != "" { print $3 }' | sed -E 's/^(go|v)//' | sort -t. -k1,1n -k2,2n -k3,3n | tail -1)"
	if [ -z "$newest" ]; then
		echo "vulncheck: $tool_count toolchain-class finding(s) at $pin, the pinned toolchain, in $modules; no standard-library fix is published yet, so the pin cannot lag one"
	else
		# The lag in patch releases of the pin's minor; a fix only in a newer minor
		# exceeds any ceiling, since the pinned minor no longer receives it.
		lag="$(awk -v p="${pin#go}" -v n="$newest" 'BEGIN {
			split(p, pa, "."); split(n, na, ".")
			if (na[1] + 0 > pa[1] + 0 || na[2] + 0 > pa[2] + 0) { print "minor"; exit }
			d = na[3] - pa[3]; if (d < 0) d = 0; print d }')"
		if [ "$lag" = "minor" ]; then
			echo "vulncheck: $tool_count toolchain-class finding(s) at $pin, the pinned toolchain, in $modules; the newest fix is go$newest, a newer minor than the pin's"
			echo "vulncheck: FAILING — go.mod pins $pin and the newest fix, go$newest, is in a newer minor; the pinned minor no longer receives it, past any ceiling; raise the toolchain line" >&2
			status=1
		else
			echo "vulncheck: $tool_count toolchain-class finding(s) at $pin, the pinned toolchain, in $modules; the newest fix is go$newest and the pin lags it by $lag patch release(s) against a ceiling of $CEILING"
			if [ "$lag" -gt "$CEILING" ]; then
				echo "vulncheck: FAILING — go.mod pins $pin and the newest toolchain-class fix is go$newest, $lag patch release(s) behind against a ceiling of $CEILING; raise the toolchain line" >&2
				status=1
			fi
		fi
	fi
else
	echo "vulncheck: 0 toolchain-class finding(s) at $pin, the pinned toolchain"
fi

if [ -n "$third" ]; then
	echo "vulncheck: FAILING — a module this module requires carries a known vulnerability:" >&2
	while IFS=$'\t' read -r id mod fixed; do
		aliases="$(printf '%s\n' "$rows" | awk -F'\t' -v id="$id" '$1 == "ALIAS" && $2 == id { print $3 }' | sort -u | paste -sd, - | sed 's/,/, /g')"
		echo "         $id (${aliases:-no alias}) in $mod, fixed in ${fixed:-no published fix}" >&2
	done <<<"$third"
	echo "         Bump the requirement past the fixed version; a Dependabot banner is not a gate." >&2
	status=1
fi

[ "$status" -eq 0 ] && echo "vulncheck: 0 requirement finding(s)"
exit "$status"
