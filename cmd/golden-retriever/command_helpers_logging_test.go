package main

import "testing"

func TestFormatProgressMessageSeverityColors(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want string
	}{
		{
			name: "high",
			msg:  "scan:vuln package=left-pad@1.3.0 severity=high ids=GHSA-123 provider=trivy",
			want: ansiRed + "progress scan:vuln package=left-pad@1.3.0 severity=high ids=GHSA-123 provider=trivy" + ansiReset,
		},
		{
			name: "critical",
			msg:  "scan:vuln package=left-pad@1.3.0 severity=critical ids=GHSA-123 provider=trivy",
			want: ansiRed + "progress scan:vuln package=left-pad@1.3.0 severity=critical ids=GHSA-123 provider=trivy" + ansiReset,
		},
		{
			name: "medium",
			msg:  "scan:vuln package=left-pad@1.3.0 severity=medium ids=GHSA-123 provider=trivy",
			want: ansiYellow + "progress scan:vuln package=left-pad@1.3.0 severity=medium ids=GHSA-123 provider=trivy" + ansiReset,
		},
		{
			name: "low",
			msg:  "scan:vuln package=left-pad@1.3.0 severity=low ids=GHSA-123 provider=trivy",
			want: ansiBlue + "progress scan:vuln package=left-pad@1.3.0 severity=low ids=GHSA-123 provider=trivy" + ansiReset,
		},
		{
			name: "unknown",
			msg:  "scan:vuln package=left-pad@1.3.0 severity=unknown ids=GHSA-123 provider=trivy",
			want: ansiMagenta + "progress scan:vuln package=left-pad@1.3.0 severity=unknown ids=GHSA-123 provider=trivy" + ansiReset,
		},
		{
			name: "default missing severity",
			msg:  "scan:vuln package=left-pad@1.3.0 ids=GHSA-123 provider=trivy",
			want: ansiRed + "progress scan:vuln package=left-pad@1.3.0 ids=GHSA-123 provider=trivy" + ansiReset,
		},
		{
			name: "non vuln",
			msg:  "trivy:db:done elapsed=2s packages=1",
			want: "progress trivy:db:done elapsed=2s packages=1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatProgressMessage(tt.msg); got != tt.want {
				t.Fatalf("formatProgressMessage(%q) = %q, want %q", tt.msg, got, tt.want)
			}
		})
	}
}

func TestProgressFieldValue(t *testing.T) {
	msg := "scan:vuln package=left-pad@1.3.0 severity=HIGH ids=GHSA-123 provider=trivy"
	if got := progressFieldValue(msg, "severity"); got != "high" {
		t.Fatalf("progressFieldValue(severity) = %q, want high", got)
	}
	if got := progressFieldValue(msg, "provider"); got != "trivy" {
		t.Fatalf("progressFieldValue(provider) = %q, want trivy", got)
	}
	if got := progressFieldValue(msg, "missing"); got != "" {
		t.Fatalf("progressFieldValue(missing) = %q, want empty", got)
	}
}
