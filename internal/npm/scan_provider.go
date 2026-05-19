package npm

import (
	"context"
	"fmt"
	"strings"
)

type vulnerabilityProvider interface {
	Name() string
	ApplyFindings(ctx context.Context, state *State, opts ScanOptions, keys []string) error
}

func applyVulnerabilityProviderFindings(ctx context.Context, state *State, opts ScanOptions, keys []string) error {
	provider, err := vulnerabilityProviderFor(opts.OSVProvider)
	if err != nil {
		return err
	}
	return provider.ApplyFindings(ctx, state, opts, keys)
}

func vulnerabilityProviderFor(raw string) (vulnerabilityProvider, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "osv-api":
		return osvAPIProvider{}, nil
	case "osv-offline":
		return osvOfflineProvider{}, nil
	case "trivy":
		return trivyProvider{}, nil
	case "trivy-offline":
		return trivyOfflineProvider{}, nil
	default:
		return nil, fmt.Errorf("unsupported scan provider %q", raw)
	}
}
