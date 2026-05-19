package npm

import (
	"fmt"
	"strings"
)

type severityLevel int

const (
	sevNone severityLevel = iota
	sevLow
	sevMedium
	sevHigh
	sevCritical
)

func parseSeverityLevel(raw string) (severityLevel, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "none":
		return sevNone, nil
	case "low":
		return sevLow, nil
	case "medium":
		return sevMedium, nil
	case "high":
		return sevHigh, nil
	case "critical":
		return sevCritical, nil
	default:
		return sevNone, fmt.Errorf("unsupported severity %q", raw)
	}
}

func parseScannerSeverity(raw string, unknown severityLevel) severityLevel {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "low":
		return sevLow
	case "medium":
		return sevMedium
	case "high":
		return sevHigh
	case "critical":
		return sevCritical
	default:
		return unknown
	}
}

func (s severityLevel) String() string {
	switch s {
	case sevLow:
		return "low"
	case sevMedium:
		return "medium"
	case sevHigh:
		return "high"
	case sevCritical:
		return "critical"
	default:
		return "none"
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
