package taprootscripts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestRenovateRegexManagers checks that each regex manager in renovate.json
// matches exactly one place in each file it names, and that the Rust versions
// they read agree with the Containerfile. Renovate silently skips a regex
// manager that matches nothing, so an edit to the RUST_VERSION lines or the
// rust FROM line would otherwise stop Rust updates without any error.
func TestRenovateRegexManagers(t *testing.T) {
	root := filepath.Join("..", "..")
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	var config struct {
		CustomManagers []struct {
			ManagerFilePatterns []string `json:"managerFilePatterns"`
			MatchStrings        []string `json:"matchStrings"`
		} `json:"customManagers"`
	}
	if err := json.Unmarshal([]byte(read("renovate.json")), &config); err != nil {
		t.Fatalf("renovate.json: %v", err)
	}
	if len(config.CustomManagers) == 0 {
		t.Fatal("renovate.json has no customManagers")
	}

	containerfile := read("Containerfile.taproot")
	rustVersion := regexp.MustCompile(`(?m)^ARG RUST_VERSION=(\S+)$`).FindStringSubmatch(containerfile)
	rustDigest := regexp.MustCompile(`(?m)^FROM docker\.io/library/rust:\S+@(sha256:[0-9a-f]{64}) `).FindStringSubmatch(containerfile)
	if rustVersion == nil || rustDigest == nil {
		t.Fatal("Containerfile.taproot has no ARG RUST_VERSION line or rust FROM line")
	}

	files := []string{"Containerfile.taproot", ".github/workflows/ci.yml"}
	matched := map[string]bool{}
	for i, manager := range config.CustomManagers {
		for _, pattern := range manager.ManagerFilePatterns {
			// Renovate writes a regular expression pattern between slashes.
			if len(pattern) < 2 || !strings.HasPrefix(pattern, "/") || !strings.HasSuffix(pattern, "/") {
				t.Fatalf("customManagers[%d]: pattern %q is not a /regex/", i, pattern)
			}
			fileRE := regexp.MustCompile(pattern[1 : len(pattern)-1])
			for _, file := range files {
				if !fileRE.MatchString(file) {
					continue
				}
				matched[file] = true
				content := read(file)
				for _, matchString := range manager.MatchStrings {
					re := regexp.MustCompile(matchString)
					all := re.FindAllStringSubmatch(content, -1)
					if len(all) != 1 {
						t.Errorf("customManagers[%d] matches %s %d times, want once", i, file, len(all))
						continue
					}
					idx := re.SubexpIndex("currentValue")
					if idx < 0 {
						t.Errorf("customManagers[%d]: match string has no currentValue group", i)
						continue
					}
					if got := all[0][idx]; got != rustVersion[1] {
						t.Errorf("customManagers[%d] reads Rust %q from %s, want RUST_VERSION %q", i, got, file, rustVersion[1])
					}
					if idx := re.SubexpIndex("currentDigest"); idx >= 0 && all[0][idx] != rustDigest[1] {
						t.Errorf("customManagers[%d] reads digest %q from %s, want the rust FROM line's %q", i, all[0][idx], file, rustDigest[1])
					}
				}
			}
		}
	}
	for _, file := range files {
		if !matched[file] {
			t.Errorf("no regex manager in renovate.json covers %s", file)
		}
	}
}
