#!/usr/bin/env bash
#
# Fails when go.mod / go.sum are not already tidy.
#
# A dependency that nothing imports is removed by the next `go mod tidy`, so a
# bare require line is not a dependency — it is a line that will be deleted.
# This check runs the tidy for real and diffs the result, which is the only way
# to prove a module's dependencies are the ones its code actually needs.
#
# Running the tidy for real means mutating the working tree, so the restore is
# unconditional. It happens in an EXIT trap, which fires on success, on a failed
# tidy, and on the Ctrl-C that would otherwise leave a tidied go.mod behind with
# the pristine copy stranded in a backup file.
#
# The backups live in a mktemp directory outside the repository, so even a
# restore that never runs cannot leave a stray file for `git add -A` to commit.
#
# Usage: scripts/tidy-check.sh   (GO=... to override the toolchain)

set -uo pipefail

GO="${GO:-go}"
root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root" || exit 1

backup_dir="$(mktemp -d)" || exit 1

restore() {
	local rc=$?
	# cp rather than mv: a second signal arriving mid-restore must not be able
	# to leave the file missing altogether.
	[ -f "$backup_dir/go.mod" ] && cp -p "$backup_dir/go.mod" go.mod
	[ -f "$backup_dir/go.sum" ] && cp -p "$backup_dir/go.sum" go.sum
	rm -rf "$backup_dir"
	return "$rc"
}
trap restore EXIT
# Turn the signals into a normal exit so the EXIT trap runs. Without this, a
# Ctrl-C during `go mod tidy` kills the script with the tree still modified.
trap 'exit 130' INT
trap 'exit 143' TERM

if ! cp -p go.mod "$backup_dir/go.mod"; then
	echo "tidy-check: could not back up go.mod" >&2
	exit 1
fi
if [ -f go.sum ] && ! cp -p go.sum "$backup_dir/go.sum"; then
	echo "tidy-check: could not back up go.sum" >&2
	exit 1
fi

if ! "$GO" mod tidy; then
	echo "tidy-check: 'go mod tidy' failed; go.mod/go.sum restored" >&2
	exit 1
fi

status=0
untidy() {
	if [ "$status" -eq 0 ]; then
		echo "tidy-check: go.mod/go.sum are not tidy; run 'go mod tidy' and commit the result" >&2
	fi
	status=1
}

for f in go.mod go.sum; do
	before="$backup_dir/$f"
	if [ -f "$f" ] && [ -f "$before" ]; then
		if ! diff -q "$before" "$f" >/dev/null; then
			untidy
			diff -u "$before" "$f"
		fi
	elif [ -f "$f" ]; then
		untidy
		echo "tidy-check: '$f' was created by 'go mod tidy'" >&2
	elif [ -f "$before" ]; then
		untidy
		echo "tidy-check: '$f' was removed by 'go mod tidy'" >&2
	fi
done

if [ "$status" -eq 0 ]; then
	echo "tidy-check: go.mod/go.sum are tidy"
fi
exit "$status"
