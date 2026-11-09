package taproot

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
)

// exampleConfigYAML is examples/taproot/config.yaml, which a first start
// writes with auth-dir moved beside the new file. TestDefaultConfigMatchesExample
// keeps the two equal.
const exampleConfigYAML = `# Upstream CLIProxyAPI settings for a taproot container.
# config.example.yaml at the repository root documents every key.
config-version: 8
server:
    # Inside the container. Publish the port on the host's Tailscale address only.
    host: "0.0.0.0"
    port: 8317
# taproot.yaml admits callers by Tailscale identity, so the proxy has no API keys.
access: {}
routing:
    # Taproot's selector: new sessions go to the account whose weekly window resets soonest.
    strategy: "soonest-reset-first"
    # Keep each client session on one account, so prompt caches stay warm.
    session-affinity: true
    session-affinity-ttl: "1h"
observability:
    logs:
        # Taproot stores request logs compressed in its data directory. Set false to keep none.
        request-log: true
        logging-to-file: false
management:
    # The access guard decides who reaches the management API and adds the key itself.
    allow-remote: true
    # Upstream's downloadable control panel is replaced by taproot's dashboard.
    disable-control-panel: true
    disable-auto-update-panel: true
oauth:
    # OAuth credential files. Back this directory up; it holds refresh tokens.
    auth-dir: "/taproot/config/auths"
`

const exampleAuthDirLine = `    auth-dir: "/taproot/config/auths"` + "\n"

// defaultTaprootYAML is the taproot.yaml a first start writes: an access
// section with no devices, so the server starts unclaimed and logs a setup code.
const defaultTaprootYAML = `# Taproot settings, written on first start. examples/taproot/taproot.yaml in
# the taproot repository documents every key. The dashboard's Access page edits
# the device and address lists, and its Settings page edits retention, the
# backup schedule, and alerts.
#
# While no device here has admin: true, taproot logs a setup code at each
# start. Open the dashboard from the device that should become the first admin
# and enter the code. To claim the server again after losing every admin
# device, remove the admin: true entries and restart taproot.
access:
`

// writeDefaultConfig writes config.yaml at configPath, and taproot.yaml where
// Setup will look for it, when config.yaml does not exist. It never replaces
// an existing file: a missing config.yaml beside an existing taproot.yaml gets
// only config.yaml. An existing config.yaml means a deployment, so nothing is
// written and Setup's check for taproot.yaml still applies.
func writeDefaultConfig(configPath string) error {
	if _, err := os.Stat(configPath); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("check %s: %w", configPath, err)
	}
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", configPath, err)
	}
	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	authLine := "    auth-dir: " + strconv.Quote(filepath.ToSlash(filepath.Join(dir, "auths"))) + "\n"
	if err := createFile(abs, strings.Replace(exampleConfigYAML, exampleAuthDirLine, authLine, 1)); err != nil {
		return err
	}
	log.Infof("taproot: %s did not exist, so taproot wrote a default config there", abs)

	tpPath := ConfigPath(abs)
	if errMkdir := os.MkdirAll(filepath.Dir(tpPath), 0o700); errMkdir != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(tpPath), errMkdir)
	}
	switch err := createFile(tpPath, defaultTaprootYAML); {
	case errors.Is(err, fs.ErrExist):
		log.Infof("taproot: kept the existing %s", tpPath)
	case err != nil:
		return err
	default:
		log.Infof("taproot: wrote %s with no devices; the server starts unclaimed and logs a setup code", tpPath)
	}
	return nil
}

// createFile writes a new file and fails with fs.ErrExist if one is there,
// so a file created since the check above is never replaced.
func createFile(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
