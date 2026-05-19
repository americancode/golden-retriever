package npm

func recomputeScanReport(state *State, keys []string, source string) ScanReport {
	report := ScanReport{Total: len(keys)}
	for _, key := range keys {
		rec, _ := getStateRecord(state, key, source)
		switch rec.ScanStatus {
		case "pass":
			report.Passed++
		case "fail":
			report.Failed++
			report.Findings = append(report.Findings, ScanFinding{
				Package:          rec.Name + "@" + rec.Version,
				Status:           "fail",
				Reason:           rec.ScanReason,
				VulnURLs:         append([]string(nil), rec.ScanVulnURLs...),
				VulnDescriptions: append([]string(nil), rec.ScanVulnDescriptions...),
				ScannedAt:        rec.ScannedAt,
			})
		default:
			report.Errors++
		}
	}
	return report
}
