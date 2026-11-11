package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A save truncates the file before writing it, so a load that reads during the
// write used to parse an empty or partial file and return a config without the
// saved values. The comment block spans several pages so a partial read is
// possible as well as an empty one.
func TestLoadConfigDuringSaveSeesWholeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	padding := strings.Repeat("# "+strings.Repeat("x", 98)+"\n", 100)
	if errWrite := os.WriteFile(path, []byte(padding+"port: 8317\nrequest-retry: 3\n"), 0o600); errWrite != nil {
		t.Fatalf("write config: %v", errWrite)
	}
	cfg, errLoad := LoadConfig(path)
	if errLoad != nil {
		t.Fatalf("LoadConfig() error = %v", errLoad)
	}

	const saves = 300
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < saves; i++ {
			select {
			case <-stop:
				return
			default:
			}
			cfg.Debug = i%2 == 0
			if errSave := SaveConfigPreserveComments(path, cfg); errSave != nil {
				t.Errorf("SaveConfigPreserveComments() error = %v", errSave)
				return
			}
		}
	}()
	defer func() {
		close(stop)
		<-done
	}()

	for {
		select {
		case <-done:
			return
		default:
		}
		loaded, errReload := LoadConfig(path)
		if errReload != nil {
			t.Fatalf("LoadConfig() during save error = %v", errReload)
		}
		if loaded.Port != 8317 || loaded.RequestRetry != 3 {
			t.Fatalf("LoadConfig() during save = port %d, request-retry %d; want 8317 and 3", loaded.Port, loaded.RequestRetry)
		}
	}
}

// A load that finds a plaintext secret key hashes it and writes the file back.
// That write-back is based on its own read of the file, so a read cut short by
// a concurrent save used to write the shortened file back to disk.
func TestLoadConfigHashingSecretDuringSaveKeepsWholeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	padding := strings.Repeat("# "+strings.Repeat("x", 98)+"\n", 100)
	const tail = "request-retry: 3\n"
	content := []byte("remote-management:\n  secret-key: example-secret\n" + padding + "port: 8317\n" + tail)

	for i := 0; i < 30; i++ {
		if errWrite := os.WriteFile(path, content, 0o600); errWrite != nil {
			t.Fatalf("write config: %v", errWrite)
		}
		// Save the plaintext file over and over, as management saves do, until
		// the load's write-back puts a bcrypt hash in it; keep that version.
		stop := make(chan struct{})
		writtenBack := make(chan []byte, 1)
		go func() {
			for {
				unlock := LockConfigFileWrite()
				data, errRead := os.ReadFile(path)
				if errRead == nil && bytes.Contains(data, []byte("$2a$")) {
					unlock()
					writtenBack <- data
					return
				}
				select {
				case <-stop:
					unlock()
					writtenBack <- nil
					return
				default:
				}
				errWrite := os.WriteFile(path, content, 0o600)
				unlock()
				if errWrite != nil {
					t.Errorf("save config: %v", errWrite)
					writtenBack <- nil
					return
				}
			}
		}()
		_, errLoad := LoadConfig(path)
		close(stop)
		data := <-writtenBack
		if errLoad != nil {
			t.Fatalf("LoadConfig() error = %v", errLoad)
		}
		if data == nil {
			t.Fatalf("load %d did not write the hashed secret key back", i)
		}
		if !strings.HasSuffix(string(data), tail) {
			t.Fatalf("load %d wrote back a cut-off config.yaml ending %q", i, data[max(0, len(data)-40):])
		}
	}
}
