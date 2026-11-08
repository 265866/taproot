// Package taprootscripts tests the upstream sync scripts in this directory
// against throwaway git repositories.
package taprootscripts

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const oldPath = "github.com/router-for-me/CLIProxyAPI/v8"

// gitLocationVars are the variables git reads to find a repository, its
// index, objects, or per-command configuration.
var gitLocationVars = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR",
	"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_NAMESPACE", "GIT_CEILING_DIRECTORIES", "GIT_DISCOVERY_ACROSS_FILESYSTEM",
	"GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT",
}

func scriptPath(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(name)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// repo is a throwaway git repository with a fixed identity and no user or
// system git configuration.
type repo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed")
	}
	r := &repo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	return r
}

// env is the caller's environment without the variables that point git at
// another repository. Git sets GIT_DIR and GIT_INDEX_FILE for hooks and for
// `git rebase -x`, and a fixture command run with them would act on that
// repository.
func (r *repo) env() []string {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return slices.Contains(gitLocationVars, name)
	})
	return append(env,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=alice",
		"GIT_AUTHOR_EMAIL=alice@example.com",
		"GIT_COMMITTER_NAME=alice",
		"GIT_COMMITTER_EMAIL=alice@example.com",
		"GIT_TERMINAL_PROMPT=0",
	)
}

// run runs a command in the repository and returns its combined output and
// exit code.
func (r *repo) run(name string, args ...string) (string, int) {
	r.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = r.dir
	cmd.Env = r.env()
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &exit):
		return string(out), exit.ExitCode()
	default:
		r.t.Fatalf("%s %v: %v", name, args, err)
		return "", -1
	}
}

func (r *repo) must(name string, args ...string) string {
	r.t.Helper()
	out, code := r.run(name, args...)
	if code != 0 {
		r.t.Fatalf("%s %v exited %d:\n%s", name, args, code, out)
	}
	return strings.TrimSpace(out)
}

func (r *repo) git(args ...string) string {
	r.t.Helper()
	return r.must("git", args...)
}

// lib runs a function from upstream-lib.sh.
func (r *repo) lib(fn string, args ...string) (string, int) {
	r.t.Helper()
	return r.run("bash", append([]string{"-c", `source "$0"; "$@"`, scriptPath(r.t, "upstream-lib.sh"), fn}, args...)...)
}

// commitFiles writes files (path to content; empty content deletes the file)
// and commits them on the current branch.
func (r *repo) commitFiles(msg string, files map[string]string) string {
	r.t.Helper()
	for path, content := range files {
		full := filepath.Join(r.dir, path)
		if content == "" {
			r.git("rm", "-q", "--", path)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			r.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			r.t.Fatal(err)
		}
		r.git("add", "--", path)
	}
	r.git("commit", "-q", "--allow-empty", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

func (r *repo) commitTree(tree, msg string, parents ...string) string {
	r.t.Helper()
	args := []string{"commit-tree", tree, "-m", msg}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	return r.git(args...)
}

// snapshot commits upstream commit up with the module path rewritten, the way
// sync-upstream.sh does.
func (r *repo) snapshot(up string, previous ...string) string {
	r.t.Helper()
	tree, code := r.lib("rewrite_tree", up)
	if code != 0 {
		r.t.Fatalf("rewrite_tree %s exited %d:\n%s", up, code, tree)
	}
	lines := strings.Split(strings.TrimSpace(tree), "\n")
	return r.commitTree(lines[len(lines)-1], "chore(upstream): rewrite\n\nTaproot-Upstream: "+up, append([]string{up}, previous...)...)
}

func goVersion(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimPrefix(strings.Fields(string(out))[0], "go")
}

// goMod names the running toolchain, so the rewrite needs no download.
func goMod(t *testing.T) string {
	return "module " + oldPath + "\n\ngo " + goVersion(t) + "\n"
}

func TestLatestSnapshotNeedsTrailerAndShape(t *testing.T) {
	r := newRepo(t)
	tree := r.git("mktree") // empty tree
	u1 := r.commitTree(tree, "upstream one")
	s1 := r.commitTree(tree, "snapshot one\n\nTaproot-Upstream: "+u1, u1)
	// The shape matches, but the line is in the body, not a trailer.
	bodyLine := r.commitTree(tree, "chore: note\n\nTaproot-Upstream: "+u1+"\n\nThis paragraph ends the message.", u1, s1)
	// A real trailer on a commit whose first parent is not the named commit.
	wrongParent := r.commitTree(tree, "fix: copy a trailer\n\nTaproot-Upstream: "+u1, bodyLine)
	// The shape matches, but the second parent is not a snapshot.
	notChained := r.commitTree(tree, "snapshot?\n\nTaproot-Upstream: "+u1, u1, wrongParent)
	// Two trailers name two commits; a snapshot holds one.
	twoTrailers := r.commitTree(tree, "snapshot?\n\nTaproot-Upstream: "+u1+"\nTaproot-Upstream: "+u1, u1, notChained)

	for _, tc := range []struct{ head, want string }{
		{s1, s1},
		{bodyLine, s1},
		{wrongParent, s1},
		{notChained, s1},
		{twoTrailers, s1},
	} {
		if got, code := r.lib("latest_snapshot", tc.head); strings.TrimSpace(got) != tc.want || code != 0 {
			t.Errorf("latest_snapshot %.9s = %q (exit %d), want %.9s", tc.head, got, code, tc.want)
		}
	}

	u2 := r.commitTree(tree, "upstream two", u1)
	s2 := r.commitTree(tree, "snapshot two\n\ntaproot-upstream: "+u2, u2, s1)
	merge := r.commitTree(tree, "merge upstream", twoTrailers, s2)
	if got, code := r.lib("latest_snapshot", merge); strings.TrimSpace(got) != s2 || code != 0 {
		t.Errorf("latest_snapshot after the second sync = %q (exit %d), want %.9s", got, code, s2)
	}

	if got, code := r.lib("latest_snapshot", u2); code == 0 {
		t.Errorf("latest_snapshot of a history without snapshots = %q, want failure", got)
	}
}

func TestGoToolchain(t *testing.T) {
	r := newRepo(t)
	for _, tc := range []struct{ goMod, want string }{
		{"module m\n\ngo 1.26.10\n", "go1.26.10"},
		{"module m\n\ngo 1.26.10\n\ntoolchain go1.27.3\n", "go1.27.3"},
		{"module m\n\ngo 1.22\n", "go1.22.0"},
		{"module m\n\ngo 1.20\n", "go1.20"},
		{"module m\n\ngo 1.22rc1\n", "go1.22rc1"},
	} {
		if err := os.WriteFile(filepath.Join(r.dir, "go.mod"), []byte(tc.goMod), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, code := r.lib("go_toolchain"); strings.TrimSpace(got) != tc.want || code != 0 {
			t.Errorf("go_toolchain for %q = %q (exit %d), want %s", tc.goMod, got, code, tc.want)
		}
	}
	if err := os.WriteFile(filepath.Join(r.dir, "go.mod"), []byte("module m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, code := r.lib("go_toolchain"); code == 0 {
		t.Errorf("go_toolchain without a go line = %q, want failure", got)
	}
}

func TestNewInDeletedCategories(t *testing.T) {
	r := newRepo(t)
	base := r.commitFiles("base", map[string]string{"README.md": "a\n", "docs/kept.md": "a\n"})
	head := r.commitFiles("upstream adds files", map[string]string{
		"README.md":               "b\n",
		"docs/kept.md":            "b\n",
		"docs/new-guide.md":       "x\n",
		"docs/guides/nested.md":   "x\n",
		"docs/diagram.png":        "x\n",
		".github/workflows/x.yml": "x\n",
		".github/FUNDING.yml":     "x\n",
		"README_KO.md":            "x\n",
		"Dockerfile.cuda":         "x\n",
		"build.Dockerfile":        "x\n",
		"docker-compose.dev.yaml": "x\n",
		"compose.yml":             "x\n",
		"docker-build.sh":         "x\n",
		"assets/sponsor.png":      "x\n",
		".env.home.example":       "x\n",
		"internal/x/Dockerfile":   "x\n",
		"internal/x/x.go":         "package x\n",
		"examples/README_CN.md":   "x\n",
	})
	got, code := r.lib("new_in_deleted_categories", base, head)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, got)
	}
	want := []string{
		".env.home.example",
		".github/FUNDING.yml",
		".github/workflows/x.yml",
		"Dockerfile.cuda",
		"README_KO.md",
		"assets/sponsor.png",
		"build.Dockerfile",
		"compose.yml",
		"docker-build.sh",
		"docker-compose.dev.yaml",
		"docs/new-guide.md",
	}
	lines := strings.Fields(got)
	slices.Sort(lines)
	if !slices.Equal(lines, want) {
		t.Errorf("flagged\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}

	if got, code := r.lib("new_in_deleted_categories", head, head); strings.TrimSpace(got) != "" || code != 0 {
		t.Errorf("no new files: %q (exit %d)", got, code)
	}
}

// In one import block, upstream's sorted order puts github.com/a before its
// own path, and the rewritten path sorts before github.com/a, so gofmt must
// reorder the block.
const reorderedImports = `package a

import (
	"github.com/a/b"
	"` + oldPath + `/internal/x"
)

var _, _ = b.B, x.X
`

func TestCheckRewrite(t *testing.T) {
	r := newRepo(t)
	up := r.commitFiles("upstream", map[string]string{"go.mod": goMod(t), "a.go": reorderedImports})
	good := r.snapshot(up)
	r.git("reset", "-q", "--hard", good)

	out := r.must("bash", scriptPath(t, "check-rewrite.sh"))
	if !strings.Contains(out, "reproduces snapshot "+good[:9]) {
		t.Errorf("check-rewrite on a good snapshot:\n%s", out)
	}

	// A snapshot made without re-sorting imports, as a different gofmt might
	// leave it.
	r.git("checkout", "-q", "--detach", up)
	r.must("perl", "-pi", "-e", `s#\Q`+oldPath+`\E#github.com/265866/taproot#g`, "a.go", "go.mod")
	r.git("add", "-u")
	bad := r.commitTree(r.git("write-tree"), "chore(upstream): rewrite\n\nTaproot-Upstream: "+up, up)
	out, code := r.run("bash", scriptPath(t, "check-rewrite.sh"), bad)
	if code == 0 || !strings.Contains(out, "a.go") {
		t.Errorf("check-rewrite on a snapshot with unsorted imports exited %d:\n%s", code, out)
	}
	if list := r.git("worktree", "list", "--porcelain"); strings.Count(list, "worktree ") != 1 {
		t.Errorf("check-rewrite left a worktree behind:\n%s", list)
	}
}

// syncFixture is an upstream repository with one commit and a taproot
// repository synced to it, which deleted the upstream Dockerfile.
func syncFixture(t *testing.T) (up, tp *repo) {
	t.Helper()
	up = newRepo(t)
	u1 := up.commitFiles("upstream one", map[string]string{
		"go.mod":     goMod(t),
		"main.go":    "package main\n\nimport _ \"" + oldPath + "/x\"\n\nfunc main() {}\n",
		"x/x.go":     "package x\n",
		"Dockerfile": "FROM scratch\n",
	})
	tp = newRepo(t)
	tp.git("remote", "add", "upstream", up.dir)
	tp.git("fetch", "-q", "upstream", "main")
	s1 := tp.snapshot(u1)
	tp.git("reset", "-q", "--hard", s1)
	tp.commitFiles("chore: delete the Dockerfile", map[string]string{"Dockerfile": ""})
	return up, tp
}

func TestSyncStopsOnNewFilesOfDeletedKinds(t *testing.T) {
	up, tp := syncFixture(t)
	up.commitFiles("upstream two", map[string]string{
		"Dockerfile":              "FROM scratch\nCOPY . .\n",
		"docs/new-guide.md":       "# Guide\n",
		".github/workflows/x.yml": "on: push\n",
		"x/y.go":                  "package x\n",
	})
	sync := scriptPath(t, "sync-upstream.sh")
	before := tp.git("rev-parse", "HEAD")

	out, code := tp.run("bash", sync, "--dry-run")
	if code != 1 {
		t.Errorf("dry run exited %d, want 1:\n%s", code, out)
	}
	for _, want := range []string{"reproduces snapshot", "would stay deleted:\n  Dockerfile", "  .github/workflows/x.yml\n  docs/new-guide.md\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "x/y.go") || strings.Contains(out, "would conflict") {
		t.Errorf("dry run flagged an ordinary file or a conflict:\n%s", out)
	}

	out, code = tp.run("bash", sync, "--no-fetch")
	if code != 1 || !strings.Contains(out, "Merge stopped before committing") || !strings.Contains(out, "docs/new-guide.md") {
		t.Errorf("sync exited %d:\n%s", code, out)
	}
	if head := tp.git("rev-parse", "HEAD"); head != before {
		t.Errorf("sync committed %s; it must leave the decision to a person", head)
	}
	tp.git("rev-parse", "--verify", "MERGE_HEAD")
	if _, err := os.Stat(filepath.Join(tp.dir, "Dockerfile")); !os.IsNotExist(err) {
		t.Errorf("the deleted Dockerfile came back: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tp.dir, "docs/new-guide.md")); err != nil {
		t.Errorf("the new guide is not staged for a decision: %v", err)
	}
}

func TestSyncMergesAndRunsTheCIChecks(t *testing.T) {
	up, tp := syncFixture(t)
	u2 := up.commitFiles("upstream two", map[string]string{"x/y.go": "package x\n"})

	out, code := tp.run("bash", scriptPath(t, "sync-upstream.sh"))
	// The fixture has no web/ directory, so that check fails and the script
	// exits 1 with the merge in place.
	if code != 1 {
		t.Errorf("sync exited %d, want 1:\n%s", code, out)
	}
	for _, want := range []string{
		"PASS  gofmt",
		"PASS  go build ./...",
		"PASS  GOOS=linux go vet ./...",
		"PASS  go test ./...",
		"FAIL  web: bun run verify",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sync output lacks %q:\n%s", want, out)
		}
	}
	parents := strings.Fields(tp.git("log", "-1", "--format=%P"))
	if len(parents) != 2 {
		t.Fatalf("HEAD is not a merge: %v", parents)
	}
	if got := tp.git("log", "-1", "--format=%(trailers:key=Taproot-Upstream,valueonly)", parents[1]); got != u2 {
		t.Errorf("merged snapshot names upstream %q, want %s", got, u2)
	}
	if got, code := tp.lib("latest_snapshot", "HEAD"); strings.TrimSpace(got) != parents[1] || code != 0 {
		t.Errorf("latest_snapshot after the sync = %q, want %s", got, parents[1])
	}
	if strings.Contains(tp.git("show", "HEAD:main.go"), oldPath) {
		t.Error("the merge brought back upstream's module path")
	}
}
