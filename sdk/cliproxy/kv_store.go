package cliproxy

import (
	"github.com/265866/taproot/internal/config"
	"github.com/265866/taproot/internal/home"
	"github.com/265866/taproot/internal/kvstore"
	log "github.com/sirupsen/logrus"
)

func (s *Service) applyKVStoreConfig(cfg *config.Config) {
	if s == nil {
		if errShutdown := kvstore.ShutdownLocal(); errShutdown != nil {
			log.Errorf("failed to shutdown local kv store: %v", errShutdown)
		}
		return
	}
	s.kvStoreManaged.Store(true)
	if cfg == nil || cfg.Home.Enabled || home.Current() != nil || cfg.KVStore.Disabled {
		if errShutdown := kvstore.ShutdownLocal(); errShutdown != nil {
			log.Errorf("failed to shutdown local kv store: %v", errShutdown)
		}
		return
	}
	_ = kvstore.ConfigureLocal(true, cfg.EffectiveKVStorePath())
}

func (s *Service) shutdownKVStore() error {
	if s == nil {
		return nil
	}
	s.kvStoreManaged.Store(false)
	return kvstore.ShutdownLocal()
}
