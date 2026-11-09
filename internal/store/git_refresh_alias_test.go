package store

import (
	"path/filepath"
	"testing"

	"github.com/265866/taproot/internal/auth/codex"
)

func TestGitRefreshJournalCoordinatesRepositoryAliases(t *testing.T) {
	root := t.TempDir()
	setupGitRemoteRepository(t, root, "main", testBranchSpec{name: "main", contents: "initial\n"})
	remote := filepath.Join(root, "seed")
	journals := []codex.RefreshJournal{
		NewGitTokenStore(remote, "", "", "main"),
		NewGitTokenStore(filepath.Join(remote, ".git"), "", "", "main"),
	}
	exerciseSharedRefreshJournal(t, journals)
}
