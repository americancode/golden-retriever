package main

import (
	"path/filepath"
	"time"

	"golden-retriever/internal/npm"
)

func newClient(input, registry, npmrc, metadataCache string, metadataCacheTTL time.Duration, metadataRetries int) (*npm.Client, error) {
	cfg, err := npm.DiscoverConfig(filepath.Dir(input), npmrc)
	if err != nil {
		return nil, err
	}
	if registry != "" {
		cfg.Registry = registry
		cfg.ApplyEnvAuthForRegistry(registry)
	}
	client := npm.NewClientWithConfig(cfg)
	client.CacheDir = metadataCache
	client.CacheTTL = metadataCacheTTL
	client.PackumentRetries = metadataRetries
	client.Offline = false
	return client, nil
}

func newTargetClient(input, registry, npmrc string, metadataRetries int, insecureSkipVerify bool) (*npm.Client, error) {
	client, err := newClient(input, registry, npmrc, "", 0, metadataRetries)
	if err != nil {
		return nil, err
	}
	client.SetInsecureSkipVerify(insecureSkipVerify)
	return client, nil
}
