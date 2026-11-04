#!/usr/bin/env bash
# Merges upstream/main into the current branch and runs taproot's checks.
# It never pushes. A file taproot deleted that upstream changed stays deleted;
# every other conflict stops the merge for a person to resolve.
#
#   scripts/taproot/sync-upstream.sh               fetch, merge, check
#   scripts/taproot/sync-upstream.sh --dry-run     fetch and report incoming commits and conflicts only
#   scripts/taproot/sync-upstream.sh --no-fetch    use the upstream/main already fetched
#   scripts/taproot/sync-upstream.sh --checks-only run the checks on HEAD, e.g. after resolving conflicts
set -euo pipefail

remote=upstream
branch=main
dry_run=0
fetch=1
checks_only=0
for arg in "$@"; do
	case "$arg" in
	--dry-run) dry_run=1 ;;
	--no-fetch) fetch=0 ;;
	--checks-only) checks_only=1 ;;
	-h | --help)
		sed -n '2,10p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
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
	local unformatted
	unformatted="$(git ls-files -z -- '*.go' | xargs -0 gofmt -l)"
	if [[ -n "$unformatted" ]]; then
		printf '%s\n' "$unformatted"
		return 1
	fi
}

build_server() {
	local out status=0
	out="$(mktemp -d)"
	go build -o "$out/taproot" ./cmd/server || status=$?
	rm -rf "$out"
	return "$status"
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

run_checks() {
	check "gofmt" gofmt_clean
	check "go build ./cmd/server" build_server
	check "go vet ./internal/taproot/..." go vet ./internal/taproot/...
	check "go test ./internal/taproot/... ./sdk/..." go test -count=1 ./internal/taproot/... ./sdk/...
	printf '\n== Summary\n'
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
git rev-parse --verify --quiet "$target^{commit}" >/dev/null || {
	printf '%s does not exist; fetch it first\n' "$target" >&2
	exit 1
}

current="$(git rev-parse --abbrev-ref HEAD)"
before="$(git rev-parse HEAD)"
incoming="$(git rev-list --count "HEAD..$target")"
printf 'Branch %s at %s; %s commit(s) incoming from %s.\n' "$current" "$(git rev-parse --short HEAD)" "$incoming" "$target"
if ((incoming == 0)); then
	echo "Already up to date."
	exit 0
fi

if ((dry_run)); then
	git log --oneline --no-decorate "HEAD..$target" | head -n 20
	tree="$(mktemp)"
	trap 'rm -f "$tree"' EXIT
	status=0
	git merge-tree --write-tree -z --no-messages HEAD "$target" >"$tree" || status=$?
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
	if [[ -n "$conflicts" ]]; then
		echo "Dry run: the merge would conflict in:"
		printf '%s\n' "$conflicts" | sed 's/^/  /'
		exit 1
	fi
	echo "Dry run: the merge would apply cleanly."
	exit 0
fi

if ! git merge --no-edit --no-ff "$target"; then
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
		echo "Resolve them and commit, then run $0 --checks-only. Or run 'git merge --abort' to undo."
		exit 1
	fi
	git commit --quiet --no-edit
fi

run_checks
printf 'Merged %s commit(s) from %s into %s (%s..%s).\n' "$incoming" "$target" "$current" "$(git rev-parse --short "$before")" "$(git rev-parse --short HEAD)"
printf '%s\n' "${results[@]}"
if ((failed)); then
	echo "Some checks failed. The merge commit is in place; fix forward, or undo it with: git reset --hard $before"
	exit 1
fi
echo "All checks passed. Nothing was pushed."
