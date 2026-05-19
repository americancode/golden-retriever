package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	ansiReset   = "\x1b[0m"
	ansiRed     = "\x1b[31m"
	ansiYellow  = "\x1b[33m"
	ansiBlue    = "\x1b[34m"
	ansiMagenta = "\x1b[35m"
)

func newTraceLogger(enabled bool) func(format string, args ...any) {
	if !enabled {
		return func(string, ...any) {}
	}
	return func(format string, args ...any) {
		ts := time.Now().UTC().Format(time.RFC3339)
		fmt.Fprintf(os.Stderr, "trace time=%s %s\n", ts, fmt.Sprintf(format, args...))
	}
}

func newProgressLogger(enabled bool) func(format string, args ...any) {
	if !enabled {
		return nil
	}
	return func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		fmt.Fprintf(os.Stderr, "%s\n", formatProgressMessage(msg))
	}
}

func formatProgressMessage(msg string) string {
	line := "progress " + msg
	switch {
	case strings.HasPrefix(msg, "publish:fail "):
		return ansiRed + line + ansiReset
	case strings.HasPrefix(msg, "scan:exception "):
		return ansiMagenta + formatScanEvent(msg, "suppressed") + ansiReset
	case strings.HasPrefix(msg, "scan:vuln-json "):
		formatted := formatScanFindingJSON(msg)
		switch progressSeverityColor(msg) {
		case "":
			return formatted
		default:
			return progressSeverityColor(msg) + formatted + ansiReset
		}
	case strings.HasPrefix(msg, "scan:vuln "):
		formatted := formatScanEvent(msg, "vulnerability")
		switch progressSeverityColor(msg) {
		case "":
			return formatted
		default:
			return progressSeverityColor(msg) + formatted + ansiReset
		}
	default:
		return line
	}
}

func formatScanFindingJSON(msg string) string {
	finding := progressJSONValue(msg, "finding")
	if finding == "" {
		return "progress {}"
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(finding), "", "  "); err != nil {
		return "progress " + finding
	}
	return "progress " + pretty.String()
}

func formatScanEvent(msg, label string) string {
	pkg := progressFieldValue(msg, "package")
	sev := progressFieldValue(msg, "severity")
	ids := progressFieldValue(msg, "ids")
	provider := progressFieldValue(msg, "provider")
	reason := progressFieldValue(msg, "reason")
	line := fmt.Sprintf("progress %s", label)
	if pkg != "" {
		line += fmt.Sprintf(" package=%s", pkg)
	}
	if sev != "" {
		line += fmt.Sprintf(" severity=%s", sev)
	}
	if ids != "" {
		line += fmt.Sprintf(" ids=%s", ids)
	}
	if provider != "" {
		line += fmt.Sprintf(" provider=%s", provider)
	}
	if reason != "" {
		line += fmt.Sprintf(" reason=%s", reason)
	}
	return line
}

func progressSeverityColor(msg string) string {
	sev := progressFieldValue(msg, "severity")
	switch sev {
	case "critical", "high":
		return ansiRed
	case "medium":
		return ansiYellow
	case "low":
		return ansiBlue
	case "unknown", "none":
		return ansiMagenta
	default:
		return ansiRed
	}
}

func progressFieldValue(msg, key string) string {
	marker := key + "="
	start := strings.Index(msg, marker)
	if start < 0 {
		return ""
	}
	value := msg[start+len(marker):]
	if idx := strings.IndexByte(value, ' '); idx >= 0 {
		value = value[:idx]
	}
	return strings.ToLower(strings.Trim(strings.TrimSpace(value), `"`))
}

func progressJSONValue(msg, key string) string {
	marker := key + "="
	start := strings.Index(msg, marker)
	if start < 0 {
		return ""
	}
	return strings.TrimSpace(msg[start+len(marker):])
}

func pickProgressLogger(traceEnabled bool, tracef, progressf func(format string, args ...any)) func(format string, args ...any) {
	if traceEnabled {
		return tracef
	}
	if progressf != nil {
		return progressf
	}
	return tracef
}
