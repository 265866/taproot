package backup

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func countByReason(entries []Entry) map[Reason]int {
	out := map[Reason]int{}
	for _, e := range entries {
		out[e.Reason]++
	}
	return out
}

func hasID(entries []Entry, id string) bool {
	for _, e := range entries {
		if e.ID == id {
			return true
		}
	}
	return false
}

// TestManualBackupsDoNotEvictScheduled covers a burst of "back up now"
// clicks, which used to prune the whole scheduled history.
func TestManualBackupsDoNotEvictScheduled(t *testing.T) {
	f := newFixture(t, Config{Keep: 2})
	ctx := context.Background()
	var scheduled []string
	for n := 0; n < 3; n++ {
		e, err := f.b.Create(ctx, ReasonScheduled)
		if err != nil {
			t.Fatal(err)
		}
		scheduled = append(scheduled, e.ID)
		f.clock = f.clock.Add(24 * time.Hour)
	}
	var manual []string
	for n := 0; n < 5; n++ {
		manual = append(manual, f.create(t).ID)
		f.clock = f.clock.Add(time.Minute)
	}
	got := f.list(t)
	if counts := countByReason(got); counts[ReasonScheduled] != 2 || counts[ReasonManual] != 2 || len(got) != 4 {
		t.Fatalf("after manual burst: %+v", got)
	}
	for _, id := range append(scheduled[1:], manual[3:]...) {
		if !hasID(got, id) {
			t.Errorf("%s pruned, want kept", id)
		}
	}
}

// TestRestoreKeepsItsSource covers restoring the oldest kept archive: the
// pre-restore backup it writes must not prune the archive being restored.
func TestRestoreKeepsItsSource(t *testing.T) {
	f := newFixture(t, Config{Keep: 2})
	ctx := context.Background()
	oldest, err := f.b.Create(ctx, ReasonScheduled)
	if err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(24 * time.Hour)
	if _, err := f.b.Create(ctx, ReasonScheduled); err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(time.Hour)
	res, err := f.b.Restore(ctx, oldest.ID, RestoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.list(t); !hasID(got, oldest.ID) || !hasID(got, res.PreRestoreBackupID) {
		t.Fatalf("after restoring the oldest: %+v", got)
	}

	// With keep=1, restoring a pre-restore archive writes a newer one of the
	// same reason; the source still stays.
	if err := f.b.SetSchedule("off", 1); err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(time.Hour)
	f.restarted()
	res2, err := f.b.Restore(ctx, res.PreRestoreBackupID, RestoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.list(t); !hasID(got, res.PreRestoreBackupID) || !hasID(got, res2.PreRestoreBackupID) {
		t.Fatalf("after restoring a pre-restore archive: %+v", got)
	}
}

// TestRestoreFailurePutsFilesBackAndLeavesDatabase covers a restore that
// fails while replacing auth files: no database swap may be pending, and the
// files it already replaced go back to their pre-restore contents.
func TestRestoreFailurePutsFilesBackAndLeavesDatabase(t *testing.T) {
	f := newFixture(t, Config{})
	e := f.create(t)

	writeFile(t, f.paths.ConfigFile, "port: 9999\n")
	blocked := filepath.Join(f.paths.AuthDir, "claude-b.json")
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	// A non-empty directory where the auth file goes makes its write fail.
	writeFile(t, filepath.Join(blocked, "keep"), "x")
	if _, err := f.db.Exec(`UPDATE probe SET v = 'after'`); err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(time.Hour)

	var apiErr apiError
	if code, body := f.do(t, http.MethodPost, "/api/backups/"+e.ID+"/restore", &apiErr); code != http.StatusInternalServerError {
		t.Fatalf("restore: %d %s", code, body)
	}
	if got := readFile(t, f.paths.ConfigFile); got != "port: 9999\n" {
		t.Errorf("config after failed restore = %q, want the pre-restore contents", got)
	}
	for _, p := range []string{
		filepath.Join(f.paths.DataDir, pendingMarkerName),
		f.db.Path() + restorePendingSuffix,
		f.db.Path() + restoreTmpSuffix,
	} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s exists after failed restore: %v", filepath.Base(p), err)
		}
	}
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ApplyPendingRestore(f.paths.DataDir); err != nil {
		t.Fatal(err)
	}
	f.openDB(t)
	if got := f.probe(t); got != "after" {
		t.Fatalf("database after failed restore and restart = %q, want after", got)
	}
	select {
	case <-f.restart:
		t.Error("failed restore requested a restart")
	default:
	}
}
