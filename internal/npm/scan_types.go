package npm

import "time"

type ScanOptions struct {
	StatePath             string
	Concurrency           int
	Source                string
	BlocklistPath         string
	DenyPackagePrefix     []string
	UseOSV                bool
	OSVProvider           string
	OSVEndpoint           string
	OSVOfflineDBDir       string
	OSVAPIBatchSize       int
	OSVAPIConcurrency     int
	OSVOfflineChunkSize   int
	OSVOfflineConcurrency int
	OSVOfflineRetryFailed bool
	TrivyOfflineScan      bool
	TrivySkipDBUpdate     bool
	TrivyChunkSize        int
	TrivyConcurrency      int
	MinSeverity           string
	UnknownSeverity       string
	ExceptionsPath        string
	Progress              func(format string, args ...any)
}

type ScanReport struct {
	Total    int           `json:"total"`
	Passed   int           `json:"passed"`
	Failed   int           `json:"failed"`
	Errors   int           `json:"errors"`
	Findings []ScanFinding `json:"findings,omitempty"`
	Elapsed  time.Duration `json:"elapsed"`
}

type ScanFinding struct {
	Package          string    `json:"package"`
	Status           string    `json:"status"`
	Reason           string    `json:"reason"`
	VulnURLs         []string  `json:"vulnUrls,omitempty"`
	VulnDescriptions []string  `json:"vulnDescriptions,omitempty"`
	ScannedAt        time.Time `json:"scannedAt,omitempty"`
}

type ScanExceptionFile struct {
	Exceptions []ScanException `json:"exceptions"`
}

type ScanBlocklistFile struct {
	Packages        []string `json:"packages"`
	PackageVersions []string `json:"packageVersions"`
	PackagePrefixes []string `json:"packagePrefixes"`
}

type ScanException struct {
	Package   string `json:"package,omitempty"` // name or name@version
	VulnID    string `json:"vulnId,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
	Reason    string `json:"reason,omitempty"`
}
