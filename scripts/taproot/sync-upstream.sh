#!/usr/bin/env bash
# Merges upstream/main into the current branch and runs taproot's checks.
# It never pushes.
#
#   scripts/taproot/sync-upstream.sh            fetch, merge, check
#   scripts/taproot/sync-upstream.sh --dry-run  fetch and report incoming commits and conflicts only
#   scripts/taproot/sync-upstream.sh --no-fetch use the upstream/main already fetched
set -euo pipefail

remote=upstream
branch=main
dry_run=0
fetch=1
for arg in "$@"; do
	case "$arg" in
	--dry-run) dry_run=1 ;;
	--no-fetch) fetch=0 ;;
	-h | --help)
		sed -n '2,8p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
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
	if conflicts="$(git merge-tree --write-tree --name-only --no-messages HEAD "$target")"; then
		echo "Dry run: the merge would apply cleanly."
	else
		echo "Dry run: the merge would conflict in:"
		printf '%s\n' "$conflicts" | tail -n +2 | sed 's/^/  /'
		exit 1
	fi
	exit 0
fi

if ! git merge --no-edit --no-ff "$target"; then
	echo
	echo "Merge stopped on conflicts in:"
	git diff --name-only --diff-filter=U | sed 's/^/  /'
	echo "Resolve them and commit, then rerun the checks, or run 'git merge --abort' to undo."
	exit 1
fi

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
	local out
	out="$(mktemp -d)"
	go build -o "$out/taproot" ./cmd/server
	local status=$?
	rm -rf "$out"
	return "$status"
}

check "gofmt" gofmt_clean
check "go build ./cmd/server" build_server
check "go vet ./internal/taproot/..." go vet ./internal/taproot/...
check "go test ./internal/taproot/... ./sdk/..." go test -count=1 ./internal/taproot/... ./sdk/...

printf '\n== Summary\n'
printf 'Merged %s commit(s) from %s into %s (%s..%s).\n' "$incoming" "$target" "$current" "$(git rev-parse --short "$before")" "$(git rev-parse --short HEAD)"
printf '%s\n' "${results[@]}"
if ((failed)); then
	echo "Some checks failed. The merge commit is in place; fix forward or reset with: git reset --hard $before"
	exit 1
fi
echo "All checks passed. Nothing was pushed."
