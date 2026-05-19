package npm

import "context"

type trivyOfflineProvider struct{}

func (trivyOfflineProvider) Name() string {
	return "trivy-offline"
}

func (trivyOfflineProvider) ApplyFindings(ctx context.Context, state *State, opts ScanOptions, keys []string) error {
	offlineOpts := opts
	offlineOpts.TrivyOfflineScan = true
	offlineOpts.TrivySkipDBUpdate = true
	return applyTrivyFindings(ctx, state, offlineOpts, keys)
}
