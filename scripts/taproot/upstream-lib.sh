# shellcheck shell=bash
# Functions shared by the upstream sync scripts. Source it from bash; it only
# defines functions and variables. See "Upstream boundary" in
# internal/taproot/DESIGN.md.

taproot_scripts="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Marks a rewritten upstream snapshot and names the upstream commit it holds.
upstream_trailer=Taproot-Upstream

# go_toolchain prints the Go toolchain named by ./go.mod: its toolchain line,
# or else its go line as a release name (go 1.26 is go1.26.0 from Go 1.21 on).
go_toolchain() {
	awk '
		$1 == "toolchain" { tc = $2 }
		$1 == "go" && ver == "" { ver = $2 }
		END {
			if (tc != "") { print tc; exit }
			if (ver == "") exit 1
			n = split(ver, v, ".")
			if (n == 2 && v[2] ~ /^[0-9]+$/ && (v[1] > 1 || v[2] >= 21)) ver = ver ".0"
			print "go" ver
		}' go.mod
}

# pinned_gofmt prints the path of the gofmt that belongs to the toolchain
# ./go.mod names, downloading that toolchain if needed. gofmt's output changes
# between Go releases, so the installed Go must not decide it.
pinned_gofmt() {
	local toolchain goroot
	toolchain="$(go_toolchain)" || {
		echo "go.mod names no Go version" >&2
		return 1
	}
	goroot="$(GOTOOLCHAIN="$toolchain" go env GOROOT)" || {
		printf 'cannot run Go toolchain %s\n' "$toolchain" >&2
		return 1
	}
	printf '%s/bin/gofmt\n' "$goroot"
}

# is_snapshot succeeds when commit $1 has the shape sync-upstream.sh gives a
# snapshot: a Taproot-Upstream trailer that names its first parent, and either
# no other parent (the first snapshot) or a second parent that is the previous
# snapshot.
is_snapshot() {
	local line value
	local -a parents
	line="$(git log -1 --format="%P%x09%(trailers:key=$upstream_trailer,valueonly,separator=%x2C)" "$1")" || return 1
	value="${line#*$'\t'}"
	read -r -a parents <<<"${line%%$'\t'*}"
	[[ -n "$value" && "${parents[0]:-}" == "$value" ]] || return 1
	case "${#parents[@]}" in
	1) return 0 ;;
	2) is_snapshot "${parents[1]}" ;;
	*) return 1 ;;
	esac
}

# latest_snapshot prints the newest snapshot in the history of commit $1.
latest_snapshot() {
	local commit candidates
	candidates="$(git log --topo-order --format=%H -i --grep="^$upstream_trailer:" "$1")" || return 1
	for commit in $candidates; do
		if is_snapshot "$commit"; then
			printf '%s\n' "$commit"
			return 0
		fi
	done
	return 1
}

# rewrite_tree checks out commit $1 in a temporary worktree, runs
# rewrite-module-path.sh on it, and prints the resulting tree.
rewrite_tree() (
	root="$(git rev-parse --show-toplevel)" || exit 1
	scratch="$(mktemp -d)" || exit 1
	trap 'git -C "$root" worktree remove --force "$scratch/tree" >/dev/null 2>&1; rm -rf "$scratch"' EXIT
	git worktree add --quiet --detach "$scratch/tree" "$1" >&2 || exit 1
	(cd "$scratch/tree" && bash "$taproot_scripts/rewrite-module-path.sh" && git add --update) >&2 || exit 1
	git -C "$scratch/tree" write-tree
)

# check_snapshot rewrites the upstream commit that snapshot $1 holds and fails
# unless the result is the snapshot's tree. A rewrite that no longer
# reproduces its snapshots makes later merges conflict on lines upstream
# never changed.
check_snapshot() {
	local snapshot="$1" upstream tree want
	upstream="$(git rev-parse "$snapshot^1")" || return 1
	tree="$(rewrite_tree "$upstream")" || {
		printf 'rewriting upstream %s failed\n' "${upstream:0:9}" >&2
		return 1
	}
	want="$(git rev-parse "$snapshot^{tree}")" || return 1
	if [[ "$tree" != "$want" ]]; then
		printf 'Rewriting upstream %s gives tree %s, but snapshot %s has tree %s:\n' \
			"${upstream:0:9}" "${tree:0:9}" "${snapshot:0:9}" "${want:0:9}" >&2
		git diff --stat "$want" "$tree" >&2
		return 1
	fi
	printf 'Rewriting upstream %s reproduces snapshot %s (tree %s).\n' "${upstream:0:9}" "${snapshot:0:9}" "${tree:0:9}"
}

# Paths of the kinds of upstream files taproot deletes: GitHub files, the
# sponsor assets, top-level docs/*.md guides, translated READMEs, Docker build
# files, and .env examples.
deleted_categories='^(\.github/.*|assets/.*|docs/[^/]*\.md|README_[^/]*\.md|\.env[^/]*|Dockerfile[^/]*|[^/]*\.[Dd]ockerfile|docker-build[^/]*|(docker-)?compose[^/]*\.ya?ml)$'

# new_in_deleted_categories prints the files added between commits $1 and $2
# whose paths fall in those categories.
new_in_deleted_categories() {
	local added
	added="$(git -c core.quotePath=false diff --name-only --no-renames --diff-filter=A "$1" "$2")" || return 1
	printf '%s\n' "$added" | grep -E "$deleted_categories" || true
}
