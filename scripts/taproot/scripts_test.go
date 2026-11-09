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
	// bin, when set, comes first on PATH for commands run in the repository.
	bin string
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
	if r.bin != "" {
		// exec.Cmd keeps the last value of a repeated variable.
		env = append(env, "PATH="+r.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
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

// boundaryDesign is a DESIGN.md whose "Upstream boundary" section names
// entries in backticks. Names outside the section, or without backticks, do
// not count.
func boundaryDesign(entries ...string) string {
	return "# Design\n\n## Goals\n\n- `a/y.go` is mentioned here.\n\n## Upstream boundary\n\n" +
		"- a/y.go without backticks, and `README.md` with them.\n\n### Deletions\n\n" +
		strings.Join(entries, "\n") + "\n\n## Later\n\n- `a/y.go` again.\n"
}

func TestCheckUpstreamBoundary(t *testing.T) {
	r := newRepo(t)
	up := r.commitFiles("upstream", map[string]string{
		"a/x.go":         "package a\n",
		"a/y.go":         "package a\n",
		"README.md":      "upstream\n",
		"Dockerfile":     "FROM scratch\n",
		"assets/a.png":   "png\n",
		"assets/b.png":   "png\n",
		"logo/x.svg":     "<svg/>\n",
		"logo/x.png":     "png\n",
		"logo/sub/x.svg": "<svg/>\n",
	})
	snapshot := r.commitTree(r.git("rev-parse", up+"^{tree}"), "chore(upstream): rewrite\n\nTaproot-Upstream: "+up, up)
	r.git("reset", "-q", "--hard", snapshot)
	check := scriptPath(t, "check-upstream-boundary.sh")

	documented := r.commitFiles("taproot", map[string]string{
		"a/x.go":                     "package a\n\nvar X = 1\n",
		"a/taproot_x_test.go":        "package a\n",
		"README.md":                  "taproot\n",
		"Dockerfile":                 "",
		"assets/a.png":               "",
		"logo/x.svg":                 "",
		"internal/taproot/DESIGN.md": boundaryDesign("- `a/x.go`, `a/taproot_x_test.go`", "- `Dockerfile`, `assets/`, and `logo/*.svg`"),
		"internal/taproot/t.go":      "package taproot\n",
		"web/src/app.ts":             "export {}\n",
		"scripts/taproot/s.sh":       "true\n",
		"examples/taproot/e.yaml":    "a: 1\n",
		"docs/guide.md":              "guide\n",
		".github/workflows/ci.yml":   "on: push\n",
		"nix/module.nix":             "{}\n",
		"licenses/GPL-3.0.txt":       "GPL\n",
		"CHANGELOG.md":               "log\n",
		"Containerfile.taproot":      "FROM scratch\n",
	})
	out, code := r.run("bash", check)
	if code != 0 || !strings.Contains(out, "names all 6 upstream paths changed since snapshot "+snapshot[:9]) {
		t.Errorf("check on a documented commit exited %d:\n%s", code, out)
	}

	// An edit and a new file need their own entries: a directory or a glob
	// covers deletions only, a glob's * does not cross a /, and a/y.go is
	// named only outside the section or without backticks.
	undocumented := r.commitFiles("taproot: more edits", map[string]string{
		"a/y.go":         "package a\n\nvar Y = 1\n",
		"a/new.go":       "package a\n",
		"assets/b.png":   "png2\n",
		"logo/x.png":     "",
		"logo/n.svg":     "<svg/>\n",
		"logo/sub/x.svg": "",
	})
	out, code = r.run("bash", check)
	if code != 1 {
		t.Errorf("check on undocumented edits exited %d, want 1:\n%s", code, out)
	}
	var listed []string
	for line := range strings.Lines(out) {
		if path, ok := strings.CutPrefix(line, "  "); ok {
			listed = append(listed, strings.TrimSpace(path))
		}
	}
	want := []string{"A\ta/new.go", "M\ta/y.go", "M\tassets/b.png", "A\tlogo/n.svg", "D\tlogo/sub/x.svg", "D\tlogo/x.png"}
	if !slices.Equal(listed, want) {
		t.Errorf("check listed %q, want %q:\n%s", listed, want, out)
	}

	r.commitFiles("docs: name the edits", map[string]string{
		"internal/taproot/DESIGN.md": boundaryDesign(
			"- `a/x.go`, `a/taproot_x_test.go`, `a/y.go`, `a/new.go`, `assets/b.png`, `logo/n.svg`",
			"- `Dockerfile`, `assets/`, and `logo/*.svg`; `logo/x.png`; `logo/**/*.svg`",
		),
	})
	if out, code := r.run("bash", check); code != 0 {
		t.Errorf("check after documenting exited %d:\n%s", code, out)
	}
	// The commit argument checks that commit with its own DESIGN.md.
	if out, code := r.run("bash", check, undocumented); code != 1 || !strings.Contains(out, "a/new.go") {
		t.Errorf("check of %.9s exited %d:\n%s", undocumented, code, out)
	}
	if out, code := r.run("bash", check, documented); code != 0 {
		t.Errorf("check of %.9s exited %d:\n%s", documented, code, out)
	}

	r.commitFiles("docs: empty the section", map[string]string{"internal/taproot/DESIGN.md": "## Upstream boundary\n\nNothing named.\n"})
	if out, code := r.run("bash", check); code != 1 || !strings.Contains(out, "  M\ta/x.go\n") {
		t.Errorf("check with nothing named exited %d:\n%s", code, out)
	}

	r.commitFiles("docs: drop the section", map[string]string{"internal/taproot/DESIGN.md": "# Design\n\n- `a/x.go`\n"})
	if out, code := r.run("bash", check); code != 1 || !strings.Contains(out, `no "## Upstream boundary" section`) {
		t.Errorf("check without the section exited %d:\n%s", code, out)
	}
	if out, code := r.run("bash", check, up); code != 1 || !strings.Contains(out, "no rewritten upstream snapshot") {
		t.Errorf("check of a commit without a snapshot exited %d:\n%s", code, out)
	}
	if out, code := r.run("bash", check, "no-such-rev"); code != 1 || !strings.Contains(out, "no-such-rev is not a commit") || strings.Contains(out, "snapshot") {
		t.Errorf("check of an unknown revision exited %d:\n%s", code, out)
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
	tp.commitFiles("chore: delete the Dockerfile", map[string]string{
		"Dockerfile":                 "",
		"internal/taproot/DESIGN.md": boundaryDesign("- `Dockerfile` is deleted."),
	})
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
		"PASS  upstream boundary",
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

	// The checks diff HEAD against the new snapshot, so an upstream edit made
	// while resolving a merge needs an entry too.
	tp.commitFiles("taproot: edit an upstream file", map[string]string{"x/y.go": "package x\n\nvar Y = 1\n"})
	out, _ = tp.run("bash", scriptPath(t, "sync-upstream.sh"), "--checks-only")
	if !strings.Contains(out, "FAIL  upstream boundary") || !strings.Contains(out, "  M\tx/y.go\n") {
		t.Errorf("checks-only after an undocumented upstream edit:\n%s", out)
	}
}

func TestChecksOnlyPassesWithAWebDirectory(t *testing.T) {
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("bun is not installed")
	}
	r := newRepo(t)
	up := r.commitFiles("upstream", map[string]string{
		"go.mod":  goMod(t),
		"main.go": "package main\n\nfunc main() {}\n",
	})
	r.git("reset", "-q", "--hard", r.snapshot(up))
	r.commitFiles("taproot", map[string]string{
		"web/package.json":           `{"name":"web","private":true,"scripts":{"verify":"echo web-verified"}}` + "\n",
		"internal/taproot/DESIGN.md": boundaryDesign(),
	})

	out, code := r.run("bash", scriptPath(t, "sync-upstream.sh"), "--checks-only")
	if code != 0 {
		t.Errorf("checks-only exited %d, want 0:\n%s", code, out)
	}
	for _, want := range []string{"web-verified", "PASS  gofmt", "PASS  go test ./...", "PASS  upstream boundary", "PASS  web: bun run verify"} {
		if !strings.Contains(out, want) {
			t.Errorf("checks-only output lacks %q:\n%s", want, out)
		}
	}
}

// fakeNix stands in for nix in nix-refresh.sh. `run` (bun2nix) writes
// web/bun.nix from web/bun.lock, with an empty hash when the lockfile says
// "no-integrity". `build` fails like a fixed-output derivation with a stale
// hash, reporting go.sum's first line as the real hash, or with a download
// error when that line is "broken".
const fakeNix = `#!/usr/bin/env bash
set -eu
while [[ "$1" == --* ]]; do shift 2; done
case "$1" in
run)
	if grep -q no-integrity web/bun.lock; then
		printf '  "left-pad@1.3.0" = fetchurl {\n    url = "x";\n    hash = "";\n  };\n' >web/bun.nix
	else
		printf 'from %s\n' "$(cat web/bun.lock)" >web/bun.nix
	fi
	;;
build)
	want="$(head -n 1 go.sum)"
	if [[ "$want" == broken ]]; then
		echo "error: unable to download 'https://proxy.golang.org/x'" >&2
		exit 1
	fi
	printf "error: hash mismatch in fixed-output derivation '/nix/store/x-go-modules.drv':\n         specified: %s\n            got:    %s\n" "$(cat nix/vendor-hash)" "$want" >&2
	exit 1
	;;
*)
	echo "unexpected nix call: $*" >&2
	exit 2
	;;
esac
`

// nixRepo is a repository with the files nix-refresh.sh reads and writes,
// and the fake nix first on PATH.
func nixRepo(t *testing.T, files map[string]string) *repo {
	t.Helper()
	r := newRepo(t)
	r.bin = t.TempDir()
	if err := os.WriteFile(filepath.Join(r.bin, "nix"), []byte(fakeNix), 0o755); err != nil {
		t.Fatal(err)
	}
	all := map[string]string{
		"flake.nix":       "{ }\n",
		"go.sum":          "sha256-new=\n",
		"nix/vendor-hash": "sha256-old=\n",
		"web/bun.lock":    "lock two\n",
		"web/bun.nix":     "from lock one\n",
	}
	for path, content := range files {
		all[path] = content
	}
	r.commitFiles("taproot", all)
	return r
}

func (r *repo) read(path string) string {
	r.t.Helper()
	data, err := os.ReadFile(filepath.Join(r.dir, path))
	if err != nil {
		r.t.Fatal(err)
	}
	return string(data)
}

func TestNixRefreshUpdatesStaleFiles(t *testing.T) {
	r := nixRepo(t, nil)
	refresh := scriptPath(t, "nix-refresh.sh")

	out, code := r.run("bash", refresh)
	if code != 0 || !strings.Contains(out, "nix/vendor-hash: sha256-old= -> sha256-new=") {
		t.Fatalf("refresh exited %d:\n%s", code, out)
	}
	if got := r.read("nix/vendor-hash"); got != "sha256-new=\n" {
		t.Errorf("nix/vendor-hash = %q, want the hash from the mismatch", got)
	}
	if got := r.read("web/bun.nix"); got != "from lock two\n" {
		t.Errorf("web/bun.nix = %q, want it generated from web/bun.lock", got)
	}

	out, code = r.run("bash", refresh)
	if code != 0 || !strings.Contains(out, "nix/vendor-hash is current: sha256-new=") {
		t.Errorf("second refresh exited %d:\n%s", code, out)
	}
}

func TestNixRefreshKeepsTheHashWhenTheBuildFails(t *testing.T) {
	r := nixRepo(t, map[string]string{"go.sum": "broken\n"})

	out, code := r.run("bash", scriptPath(t, "nix-refresh.sh"))
	if code == 0 || !strings.Contains(out, "unable to download") || !strings.Contains(out, "without a hash mismatch") {
		t.Errorf("refresh exited %d, want a failure that shows the build log:\n%s", code, out)
	}
	if got := r.read("nix/vendor-hash"); got != "sha256-old=\n" {
		t.Errorf("nix/vendor-hash = %q after a failed build, want the old hash back", got)
	}
}

func TestNixRefreshRefusesPackagesWithoutIntegrity(t *testing.T) {
	r := nixRepo(t, map[string]string{"web/bun.lock": "no-integrity\n"})

	out, code := r.run("bash", scriptPath(t, "nix-refresh.sh"))
	if code == 0 || !strings.Contains(out, "without an integrity hash") || !strings.Contains(out, "  left-pad@1.3.0") {
		t.Errorf("refresh exited %d, want it to name the package:\n%s", code, out)
	}
	if got := r.read("nix/vendor-hash"); got != "sha256-old=\n" {
		t.Errorf("nix/vendor-hash = %q, want it untouched", got)
	}
}

func TestChecksOnlyRefreshesNixHashes(t *testing.T) {
	r := nixRepo(t, map[string]string{
		"go.mod":  goMod(t),
		"main.go": "package main\n\nfunc main() {}\n",
	})
	sync := scriptPath(t, "sync-upstream.sh")

	out, _ := r.run("bash", sync, "--checks-only")
	if !strings.Contains(out, "FAIL  nix hashes") || !strings.Contains(out, "commit them") {
		t.Errorf("checks-only with stale hashes lacks the nix failure:\n%s", out)
	}
	if got := r.read("nix/vendor-hash"); got != "sha256-new=\n" {
		t.Errorf("nix/vendor-hash = %q, want it refreshed", got)
	}

	r.git("commit", "-q", "-am", "chore(nix): refresh")
	out, _ = r.run("bash", sync, "--checks-only")
	if !strings.Contains(out, "PASS  nix hashes") {
		t.Errorf("checks-only with current hashes lacks the nix pass:\n%s", out)
	}
}
