package main

import (
	"fmt"
	"os"
	"strings"
	"time"
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
		if strings.HasPrefix(msg, "scan:vuln ") {
			fmt.Fprintf(os.Stderr, "\x1b[31mprogress %s\x1b[0m\n", msg)
			return
		}
		fmt.Fprintf(os.Stderr, "progress %s\n", msg)
	}
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
