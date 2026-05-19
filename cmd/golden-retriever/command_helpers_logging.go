package main

import (
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
	if !strings.HasPrefix(msg, "scan:vuln ") {
		return line
	}
	switch progressSeverityColor(msg) {
	case "":
		return line
	default:
		return progressSeverityColor(msg) + line + ansiReset
	}
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
	return strings.ToLower(strings.TrimSpace(value))
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
