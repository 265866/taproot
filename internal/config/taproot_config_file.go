package config

import (
	"os"
	"sync"
)

// configFileMu keeps this process's config loads from reading a config file
// while a save rewrites it. Saves truncate the file before they write it, so an
// unguarded load could parse an empty or partial file and apply a config
// without the saved values. Writes cannot rename a temporary file into place
// instead, because config.yaml is often a single-file bind mount.
var configFileMu sync.RWMutex

// ReadConfigFile reads a config file without overlapping a save of it.
func ReadConfigFile(path string) ([]byte, error) {
	configFileMu.RLock()
	defer configFileMu.RUnlock()
	return os.ReadFile(path)
}

// LockConfigFileWrite holds off other config reads and writes until the
// returned function is called. A writer holds it from the read it bases its
// change on through its write, so no other save lands in between.
func LockConfigFileWrite() (unlock func()) {
	configFileMu.Lock()
	return configFileMu.Unlock
}
