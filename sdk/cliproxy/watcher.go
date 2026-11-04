package cliproxy

import (
	"context"

	"github.com/265866/taproot/internal/watcher"
	coreauth "github.com/265866/taproot/sdk/cliproxy/auth"
	"github.com/265866/taproot/sdk/config"
)

func defaultWatcherFactory(configPath, authDir string, reload func(*config.Config)) (*WatcherWrapper, error) {
	w, err := watcher.NewWatcher(configPath, authDir, reload)
	if err != nil {
		return nil, err
	}

	return &WatcherWrapper{
		start: func(ctx context.Context) error {
			return w.Start(ctx)
		},
		stop: func() error {
			return w.Stop()
		},
		setConfig: func(cfg *config.Config) {
			w.SetConfig(cfg)
		},
		snapshotAuths: func() []*coreauth.Auth { return w.SnapshotCoreAuths() },
		snapshotAuthFile: func(path string) ([]*coreauth.Auth, error) {
			return w.SnapshotAuthFile(path)
		},
		setUpdateQueue: func(queue chan<- watcher.AuthUpdate) {
			w.SetAuthUpdateQueue(queue)
		},
		dispatchRuntimeUpdate: func(update watcher.AuthUpdate) bool {
			return w.DispatchRuntimeAuthUpdate(update)
		},
		dispatchPersistedAuth: func(update watcher.AuthUpdate) bool {
			return w.DispatchPersistedAuthUpdate(update)
		},
		dispatchPersistedAuthWithRev: func(update *watcher.AuthUpdate) (bool, uint64) {
			return w.DispatchPersistedAuthUpdateWithRevision(update)
		},
		rereadDiscardedFileSnapshot: func(auth *coreauth.Auth) {
			w.RereadDiscardedFileSnapshot(auth)
		},
		setPluginAuthParser: func(parser PluginAuthParser) {
			w.SetPluginAuthParser(parser)
		},
		reloadConfigIfChanged: func() {
			w.ReloadConfigIfChanged()
		},
	}, nil
}
