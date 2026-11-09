#!/usr/bin/env bash
# Merges upstream/main into the current branch and runs taproot's checks.
# It never pushes.
#
# Upstream's Go module path is github.com/router-for-me/CLIProxyAPI/v8 and
# taproot's is github.com/265866/taproot. The script commits upstream's tip
# with the path rewritten by rewrite-module-path.sh to the staging branch
# taproot/upstream, then merges that branch. The staging commit's second
# parent is the previous rewritten snapshot, which is already in taproot's
# history, so the merge base has the new path too and import lines do not
# conflict. Before that it checks that the rewrite still reproduces the
# previous snapshot. A file taproot deleted that upstream changed stays
# deleted. Every other conflict, and every new upstream file of a kind taproot
# deletes (such as a new docs/*.md guide), stops the merge for a person to
# decide. The checks are those of CI's go and web jobs, except golangci-lint
# and govulncheck, so they include check-upstream-boundary.sh, plus
# nix-refresh.sh when Nix is installed.
#
#   scripts/taproot/sync-upstream.sh               fetch, merge, check
#   scripts/taproot/sync-upstream.sh --dry-run     fetch and report incoming commits, conflicts, and new files to decide on;
#                                                  exits 1 when the merge would stop
#   scripts/taproot/sync-upstream.sh --no-fetch    use the upstream/main already fetched
#   scripts/taproot/sync-upstream.sh --checks-only run the checks on HEAD, e.g. after resolving conflicts
set -euo pipefail
# Command substitutions keep set -e (bash 4.4 and later). The snapshot steps
# also check their own status, for older shells.
shopt -s inherit_errexit 2>/dev/null || true
# shellcheck source=scripts/taproot/upstream-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/upstream-lib.sh"

remote=upstream
branch=main
staging=taproot/upstream
old_path=github.com/router-for-me/CLIProxyAPI/v8
dry_run=0
fetch=1
checks_only=0
for arg in "$@"; do
	case "$arg" in
	--dry-run) dry_run=1 ;;
	--no-fetch) fetch=0 ;;
	--checks-only) checks_only=1 ;;
	-h | --help)
		awk 'NR > 1 && /^#/ { sub(/^# ?/, ""); print; next } NR > 1 { exit }' "${BASH_SOURCE[0]}"
		exit 0
		;;
	*)
		printf 'unknown argument: %s\n' "$arg" >&2
		exit 2
		;;
	esac
done

root="$(git rev-parse --show-toplevel)"
cd "$root"
target="$remote/$branch"

results=()
failed=0
check() {
	local name="$1"
	shift
	printf '\n== %s\n' "$name"
	if "$@"; then
		results+=("PASS  $name")
	else
		results+=("FAIL  $name")
		failed=1
	fi
}

gofmt_clean() {
	local gofmt unformatted
	gofmt="$(pinned_gofmt)" || return 1
	unformatted="$(git ls-files -z -- '*.go' | xargs -0 "$gofmt" -l)" || return 1
	if [[ -n "$unformatted" ]]; then
		printf '%s\n' "$unformatted"
		return 1
	fi
}

web_verify() {
	(cd web && bun install --frozen-lockfile && bun run verify)
}

# nix_hashes refreshes the flake's nix/vendor-hash and web/bun.nix, which
# follow go.sum and web/bun.lock, and fails when they were stale.
nix_hashes() {
	"$taproot_scripts/nix-refresh.sh" || return 1
	if ! git diff --quiet -- nix/vendor-hash web/bun.nix; then
		echo "Refreshed nix/vendor-hash and web/bun.nix; commit them."
		return 1
	fi
}

# classify_conflicts reads unmerged index entries ("<mode> <object> <stage>\t<path>",
# NUL-separated, as git ls-files -u -z and git merge-tree -z write them) and
# prints "<kind>\t<path>" per path. The kind is "deleted" when the file existed
# at the merge base, taproot deleted it, and upstream changed it (stages 1 and
# 3 without 2), and "conflict" otherwise.
classify_conflicts() {
	tr '\0' '\n' | awk -F '\t' '
		NF >= 2 { split($1, f, " "); seen[$2] = 1; stage[$2, f[3]] = 1 }
		END {
			for (p in seen) {
				kind = (stage[p, 1] && !stage[p, 2] && stage[p, 3]) ? "deleted" : "conflict"
				print kind "\t" p
			}
		}' | sort
}

# The checks an upstream merge can break: CI's go and web jobs, except the
# Go version, golangci-lint, and govulncheck steps. HEAD is the merge commit,
# so the boundary check diffs it against the new snapshot.
run_checks() {
	check "gofmt" gofmt_clean
	check "go build ./..." go build ./...
	check "GOOS=linux go vet ./..." env GOOS=linux go vet ./...
	# TestAdvertiserAndBrowser_Integration needs real mDNS multicast, which CI
	# runners and most hosts do not have.
	check "go test ./..." go test -count=1 -skip '^TestAdvertiserAndBrowser_Integration$' ./...
	check "upstream boundary" bash "$taproot_scripts/check-upstream-boundary.sh"
	check "web: bun run verify" web_verify
	if [[ -f flake.nix ]]; then
		if command -v nix >/dev/null 2>&1; then
			check "nix hashes" nix_hashes
		else
			# CI's nix-hashes job still checks them.
			results+=("SKIP  nix hashes: nix is not installed; run scripts/taproot/nix-refresh.sh where it is")
		fi
	fi
	printf '\n== Summary\n'
}

# snapshot commits upstream's tip with the module path rewritten. Its parents
# are that tip and the previous snapshot, so later merges find the previous
# snapshot as their base. It prints the new commit.
snapshot() {
	local upstream="$1" previous="$2" tree
	tree="$(rewrite_tree "$upstream")" || {
		printf 'rewriting the module path of upstream %s failed\n' "${upstream:0:9}" >&2
		return 1
	}
	git commit-tree "$tree" -p "$upstream" -p "$previous" -m "chore(upstream): rewrite the module path of upstream ${upstream:0:9}

Upstream $upstream with the module path rewritten by
scripts/taproot/rewrite-module-path.sh.

$upstream_trailer: $upstream"
}

# print_new_files lists new upstream files of the kinds taproot deletes.
print_new_files() {
	echo "Upstream added files of kinds taproot deletes (see \"Upstream boundary\" in internal/taproot/DESIGN.md). Keep each one, or delete it with git rm:"
	printf '%s\n' "$1" | sed 's/^/  /'
}

if ((checks_only)); then
	run_checks
	printf 'Checked %s at %s.\n' "$(git rev-parse --abbrev-ref HEAD)" "$(git rev-parse --short HEAD)"
	printf '%s\n' "${results[@]}"
	exit "$failed"
fi

if ! git remote get-url "$remote" >/dev/null 2>&1; then
	printf 'no "%s" remote; add it with: git remote add %s https://github.com/router-for-me/CLIProxyAPI.git\n' "$remote" "$remote" >&2
	exit 1
fi
if [[ -n "$(git status --porcelain --untracked-files=no)" ]]; then
	echo "working tree has uncommitted changes; commit or stash them first" >&2
	exit 1
fi

if ((fetch)); then
	GIT_TERMINAL_PROMPT=0 git fetch --no-tags "$remote" "$branch"
fi
upstream="$(git rev-parse --verify --quiet "$target^{commit}")" || {
	printf '%s does not exist; fetch it first\n' "$target" >&2
	exit 1
}

previous="$(latest_snapshot HEAD)" || {
	printf 'HEAD has no rewritten upstream snapshot (a commit with a %s: trailer naming its first parent); see "Upstream boundary" in internal/taproot/DESIGN.md\n' "$upstream_trailer" >&2
	exit 1
}
merged="$(git rev-parse "$previous^1")"

current="$(git rev-parse --abbrev-ref HEAD)"
before="$(git rev-parse HEAD)"
incoming="$(git rev-list --count "$merged..$upstream")"
printf 'Branch %s at %s, last synced with upstream %s; %s commit(s) incoming from %s.\n' \
	"$current" "$(git rev-parse --short HEAD)" "$(git rev-parse --short "$merged")" "$incoming" "$target"
if ((incoming == 0)); then
	echo "Already up to date."
	exit 0
fi

# A rewrite that no longer gives the previous snapshot would also change, in
# the new snapshot, lines upstream never touched.
check_snapshot "$previous" || {
	echo "The rewrite no longer reproduces the previous snapshot; nothing was merged. Fix scripts/taproot/rewrite-module-path.sh first." >&2
	exit 1
}

echo "Rewriting the module path of upstream $(git rev-parse --short "$upstream")..."
next="$(snapshot "$upstream" "$previous")" || {
	echo "Could not build the rewritten upstream snapshot; nothing was merged." >&2
	exit 1
}
# A snapshot that still holds upstream's path would merge it in.
if git grep -q -F -e "$old_path" "$next" -- '*.go' '*go.mod' '*go.sum'; then
	printf 'The rewritten snapshot %s still mentions %s; nothing was merged.\n' "$(git rev-parse --short "$next")" "$old_path" >&2
	exit 1
fi
new_files="$(new_in_deleted_categories "$previous" "$next")"

if ((dry_run)); then
	# -n, not a pipe to head: with pipefail, git log's SIGPIPE would end the script.
	git log --oneline --no-decorate -n 20 "$merged..$upstream"
	tree="$(mktemp)"
	trap 'rm -f "$tree"' EXIT
	status=0
	git merge-tree --write-tree -z --no-messages HEAD "$next" >"$tree" || status=$?
	if ((status > 1)); then
		echo "git merge-tree failed" >&2
		exit "$status"
	fi
	classified="$(classify_conflicts <"$tree")"
	deleted="$(printf '%s\n' "$classified" | awk -F '\t' '$1 == "deleted" { print $2 }')"
	conflicts="$(printf '%s\n' "$classified" | awk -F '\t' '$1 == "conflict" { print $2 }')"
	if [[ -n "$deleted" ]]; then
		echo "Dry run: these files taproot deleted would stay deleted:"
		printf '%s\n' "$deleted" | sed 's/^/  /'
	fi
	stops=0
	if [[ -n "$new_files" ]]; then
		printf 'Dry run: the merge would stop for a decision. '
		print_new_files "$new_files"
		stops=1
	fi
	if [[ -n "$conflicts" ]]; then
		echo "Dry run: the merge would conflict in:"
		printf '%s\n' "$conflicts" | sed 's/^/  /'
		stops=1
	fi
	if ((stops)); then
		exit 1
	fi
	echo "Dry run: the merge would apply cleanly."
	exit 0
fi

git branch --force "$staging" "$next"
merge_args=(--no-edit --no-ff -m "Merge upstream $(git rev-parse --short "$upstream") into $current")
if [[ -n "$new_files" ]]; then
	merge_args+=(--no-commit)
fi
if ! git merge "${merge_args[@]}" "$staging"; then
	if ! git rev-parse --quiet --verify MERGE_HEAD >/dev/null; then
		echo "git merge failed before merging; nothing changed." >&2
		exit 1
	fi
	while IFS=$'\t' read -r kind path; do
		if [[ "$kind" == deleted ]]; then
			git rm --quiet -- "$path"
			printf 'Kept deleted (taproot removed it, upstream changed it): %s\n' "$path"
		fi
	done < <(git ls-files -u -z | classify_conflicts)
	if [[ -n "$(git diff --name-only --diff-filter=U)" ]]; then
		echo
		echo "Merge stopped on conflicts in:"
		git diff --name-only --diff-filter=U | sed 's/^/  /'
		if [[ -n "$new_files" ]]; then
			print_new_files "$new_files"
		fi
		echo "Resolve them and commit, then run $0 --checks-only. Or run 'git merge --abort' to undo."
		exit 1
	fi
	if [[ -z "$new_files" ]]; then
		git commit --quiet --no-edit
	fi
fi
if [[ -n "$new_files" ]]; then
	echo
	printf 'Merge stopped before committing. '
	print_new_files "$new_files"
	echo "Then commit and run $0 --checks-only. Or run 'git merge --abort' to undo."
	exit 1
fi

run_checks
printf 'Merged %s commit(s) from %s into %s (%s..%s).\n' "$incoming" "$target" "$current" "$(git rev-parse --short "$before")" "$(git rev-parse --short HEAD)"
printf '%s\n' "${results[@]}"
if ((failed)); then
	echo "Some checks failed. The merge commit is in place; fix forward, or undo it with: git reset --hard $before"
	exit 1
fi
echo "All checks passed. Nothing was pushed."
