package taprootscripts

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// runScript runs a script in this directory with bash from dir and returns
// its stdout, its stderr, and its exit code.
func runScript(t *testing.T, dir, name string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed")
	}
	cmd := exec.Command("bash", append([]string{scriptPath(t, name)}, args...)...)
	cmd.Dir = dir
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		code = exit.ExitCode()
	default:
		t.Fatalf("%s %v: %v", name, args, err)
	}
	return out.String(), errOut.String(), code
}

func TestReleaseVersion(t *testing.T) {
	const commit = "0123456789abcdef0123456789abcdef01234567"
	for _, tc := range []struct {
		tag, want string
	}{
		{"taproot-v0.2.0", "version=0.2.0\nprerelease=false\nlatest=true\nimage-tags=0.2.0 sha-0123456 0.2 latest\n"},
		{"taproot-v10.20.30", "version=10.20.30\nprerelease=false\nlatest=true\nimage-tags=10.20.30 sha-0123456 10.20 latest\n"},
		{"taproot-v0.2.0-rc.1", "version=0.2.0-rc.1\nprerelease=true\nlatest=false\nimage-tags=0.2.0-rc.1 sha-0123456\n"},
		{"taproot-v1.0.0-beta-2.x", "version=1.0.0-beta-2.x\nprerelease=true\nlatest=false\nimage-tags=1.0.0-beta-2.x sha-0123456\n"},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			out, errOut, code := runScript(t, t.TempDir(), "release-version.sh", tc.tag, commit)
			if code != 0 || out != tc.want {
				t.Fatalf("exit %d, stdout %q, stderr %q; want stdout %q", code, out, errOut, tc.want)
			}
		})
	}

	// Re-running an older release, or fixing an older line, moves neither X.Y
	// nor latest back.
	dir := t.TempDir()
	existing := filepath.Join(dir, "tags")
	tags := "taproot-v0.1.0\ntaproot-v0.2.3\ntaproot-v0.3.0-rc.1\ntaproot-v0.10.1\nnot-a-tag\ntaproot-v0.20.0x\n"
	if err := os.WriteFile(existing, []byte(tags), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		tag, want string
	}{
		{"taproot-v0.10.2", "version=0.10.2\nprerelease=false\nlatest=true\nimage-tags=0.10.2 sha-0123456 0.10 latest\n"},
		{"taproot-v0.10.1", "version=0.10.1\nprerelease=false\nlatest=true\nimage-tags=0.10.1 sha-0123456 0.10 latest\n"},
		{"taproot-v0.2.4", "version=0.2.4\nprerelease=false\nlatest=false\nimage-tags=0.2.4 sha-0123456 0.2\n"},
		{"taproot-v0.2.0", "version=0.2.0\nprerelease=false\nlatest=false\nimage-tags=0.2.0 sha-0123456\n"},
		{"taproot-v0.1.1", "version=0.1.1\nprerelease=false\nlatest=false\nimage-tags=0.1.1 sha-0123456 0.1\n"},
		{"taproot-v0.11.0-rc.1", "version=0.11.0-rc.1\nprerelease=true\nlatest=false\nimage-tags=0.11.0-rc.1 sha-0123456\n"},
	} {
		t.Run("after releases "+tc.tag, func(t *testing.T) {
			out, errOut, code := runScript(t, dir, "release-version.sh", tc.tag, commit, existing)
			if code != 0 || out != tc.want {
				t.Fatalf("exit %d, stdout %q, stderr %q; want stdout %q", code, out, errOut, tc.want)
			}
		})
	}

	for _, tc := range []struct{ name, tag, commit string }{
		{"no prefix", "v0.2.0", commit},
		{"no v", "taproot-0.2.0", commit},
		{"two parts", "taproot-v0.2", commit},
		{"leading zero", "taproot-v0.02.0", commit},
		{"build metadata", "taproot-v0.2.0+build.1", commit},
		{"empty prerelease part", "taproot-v0.2.0-rc..1", commit},
		{"trailing text", "taproot-v0.2.0 latest", commit},
		{"short commit", "taproot-v0.2.0", "0123456"},
		{"uppercase commit", "taproot-v0.2.0", strings.ToUpper(commit)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, code := runScript(t, t.TempDir(), "release-version.sh", tc.tag, tc.commit)
			if code != 1 || out != "" || errOut == "" {
				t.Fatalf("exit %d, stdout %q, stderr %q; want exit 1 with only an error", code, out, errOut)
			}
		})
	}
}

const releaseChangelog = `# Changelog

Notable changes to taproot.

## Unreleased

### Changed

- Access is per device.

## [0.3.0](https://github.com/265866/taproot/compare/taproot-v0.2.0...taproot-v0.3.0) (2026-12-01)


### Added

* **taproot:** a feature ([#30](https://github.com/265866/taproot/issues/30))

## [0.2.0](https://github.com/265866/taproot/compare/taproot-v0.1.0...taproot-v0.2.0) (2026-11-20)

### Fixed

* a fix

## v0.1.0

The first release.

### Added

- Everything.
`

func TestReleaseNotes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CHANGELOG.md"), []byte(releaseChangelog), 0o644); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(empty, []byte("# Changelog\n\n## Unreleased\n\n## v0.1.0\n\nThe first release.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"release-please section", []string{"0.3.0"}, "### Added\n\n* **taproot:** a feature ([#30](https://github.com/265866/taproot/issues/30))\n"},
		{"section before the hand-written one", []string{"0.2.0"}, "### Fixed\n\n* a fix\n"},
		{"hand-written v0.1.0", []string{"0.1.0"}, "The first release.\n\n### Added\n\n- Everything.\n"},
		{"prerelease without its own section", []string{"0.2.0-rc.1"}, "A release candidate for testing. It includes these changes, which are not released yet:\n\n### Changed\n\n- Access is per device.\n"},
		{"prerelease without unreleased notes", []string{"0.2.0-rc.1", empty}, "A release candidate for testing. See CHANGELOG.md for the changes in the release it leads to.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, code := runScript(t, dir, "release-notes.sh", tc.args...)
			if code != 0 || out != tc.want {
				t.Fatalf("exit %d, stderr %q, stdout:\n%s\nwant:\n%s", code, errOut, out, tc.want)
			}
		})
	}

	// A version is matched whole: 0.2 is not 0.2.0, and 0.2.0 is not 0.2.0-rc.1.
	for _, version := range []string{"0.4.0", "0.2", "Changelog"} {
		t.Run("missing "+version, func(t *testing.T) {
			out, errOut, code := runScript(t, dir, "release-notes.sh", version)
			if code != 1 || out != "" || !strings.Contains(errOut, "no section for "+version) {
				t.Fatalf("exit %d, stdout %q, stderr %q; want exit 1 naming the version", code, out, errOut)
			}
		})
	}
}

// changelog runs release-changelog.sh in the repository on a copy of
// releaseChangelog and returns the result.
func (r *repo) changelog(args ...string) (string, string, int) {
	r.t.Helper()
	path := filepath.Join(r.dir, "CHANGELOG.md")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(path, []byte(changelogBefore), 0o644); err != nil {
			r.t.Fatal(err)
		}
	}
	cmd := exec.Command("bash", append([]string{scriptPath(r.t, "release-changelog.sh")}, append(args, path)...)...)
	cmd.Dir = r.dir
	cmd.Env = append(r.env(), "GITHUB_REPOSITORY=265866/taproot")
	out, err := cmd.CombinedOutput()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		r.t.Fatal(err)
	}
	text, errRead := os.ReadFile(path)
	if errRead != nil {
		r.t.Fatal(errRead)
	}
	return string(text), string(out), code
}

const changelogBefore = `# Changelog

## Unreleased

### Changed

- A note by hand.

## v0.1.0

The first release.
`

func TestReleaseChangelog(t *testing.T) {
	r := newRepo(t)
	tree := r.git("mktree")
	u1 := r.commitTree(tree, "upstream one")
	s1 := r.commitTree(tree, "chore(upstream): rewrite\n\nTaproot-Upstream: "+u1, u1)
	t0 := r.commitTree(tree, "feat: in the first release", s1)
	r.git("tag", "taproot-v0.1.0", t0)

	t1 := r.commitTree(tree, "feat(taproot): a feature (#5)", t0)
	t2 := r.commitTree(tree, "fix!: a breaking fix", t1)
	t3 := r.commitTree(tree, "docs: a hidden change", t2)
	// A sync merges upstream commits; their feat and fix are not taproot's.
	u2 := r.commitTree(tree, "feat(codex): an upstream feature (#7149)", u1)
	u3 := r.commitTree(tree, "fix: an upstream fix", u2)
	s2 := r.commitTree(tree, "chore(upstream): rewrite\n\nTaproot-Upstream: "+u3, u3, s1)
	sync := r.commitTree(tree, "Merge upstream", t3, s2)
	t4 := r.commitTree(tree, "perf: faster\n\nBREAKING CHANGE: the format changed", sync)
	// A pull request merged with a merge commit still lists its commits.
	b1 := r.commitTree(tree, "revert: undo a change (#9)", t4)
	head := r.commitTree(tree, "Merge pull request #9 from alice/undo", t4, b1)

	ct, err := strconv.ParseInt(r.git("show", "-s", "--format=%ct", head), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	date := time.Unix(ct, 0).UTC().Format("2006-01-02")
	link := func(commit string) string {
		return "([" + commit[:7] + "](https://github.com/265866/taproot/commit/" + commit + "))"
	}
	want := `# Changelog

## Unreleased

### Changed

- A note by hand.

## [0.2.0](https://github.com/265866/taproot/compare/taproot-v0.1.0...taproot-v0.2.0) (` + date + `)

### Breaking changes

* a breaking fix ` + link(t2) + `
* faster ` + link(t4) + `

### Added

* **taproot:** a feature ([#5](https://github.com/265866/taproot/pull/5)) ` + link(t1) + `

### Fixed

* a breaking fix ` + link(t2) + `

### Performance

* faster ` + link(t4) + `

### Reverted

* undo a change ([#9](https://github.com/265866/taproot/pull/9)) ` + link(b1) + `

### Upstream

* Merges [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) up to [` + u3[:9] + `](https://github.com/router-for-me/CLIProxyAPI/commit/` + u3 + `).

## v0.1.0

The first release.
`
	got, out, code := r.changelog("0.2.0", "0.1.0", head)
	if code != 0 || got != want {
		t.Fatalf("exit %d, output %q, changelog:\n%s\nwant:\n%s", code, out, got, want)
	}
	// release-please re-runs it on every push to main; the section is replaced.
	if got, out, code := r.changelog("0.2.0", "0.1.0", head); code != 0 || got != want {
		t.Fatalf("second run: exit %d, output %q, changelog:\n%s", code, out, got)
	}
	notes, errOut, code := runScript(t, r.dir, "release-notes.sh", "0.2.0")
	if code != 0 || !strings.HasPrefix(notes, "### Breaking changes\n") || !strings.HasSuffix(notes, u3+").\n") {
		t.Fatalf("release-notes.sh 0.2.0: exit %d, stderr %q, stdout:\n%s", code, errOut, notes)
	}

	// A release after the sync, with only hidden changes, names no upstream
	// commit again and says it has no user-facing changes.
	r.git("tag", "taproot-v0.2.0", head)
	t5 := r.commitTree(tree, "chore: tidy", head)
	got, out, code = r.changelog("0.2.1", "0.2.0", t5)
	if code != 0 || !strings.Contains(got, "(https://github.com/265866/taproot/compare/taproot-v0.2.0...taproot-v0.2.1) ("+date+")\n\nNo user-facing changes.\n\n## [0.2.0]") {
		t.Fatalf("exit %d, output %q, changelog:\n%s", code, out, got)
	}

	if _, out, code := r.changelog("0.3.0", "0.2.9", t5); code != 1 || !strings.Contains(out, "taproot-v0.2.9 does not exist") {
		t.Fatalf("missing previous tag: exit %d, output %q", code, out)
	}
}
