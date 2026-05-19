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
			msg:  "scan:vuln-json severity=high provider=trivy finding={\"package\":\"left-pad@1.3.0\",\"status\":\"fail\",\"reason\":\"trivy vulnerabilities (high+): GHSA-123\",\"vulnUrls\":[\"https://example.test/GHSA-123\"],\"vulnDescriptions\":[\"GHSA-123: test vulnerability\"],\"scannedAt\":\"2026-05-19T14:00:00Z\"}",
			want: ansiRed + "progress {\n  \"package\": \"left-pad@1.3.0\",\n  \"status\": \"fail\",\n  \"reason\": \"trivy vulnerabilities (high+): GHSA-123\",\n  \"vulnUrls\": [\n    \"https://example.test/GHSA-123\"\n  ],\n  \"vulnDescriptions\": [\n    \"GHSA-123: test vulnerability\"\n  ],\n  \"scannedAt\": \"2026-05-19T14:00:00Z\"\n}" + ansiReset,
		},
		{
			name: "critical",
			msg:  "scan:vuln-json severity=critical provider=trivy finding={\"package\":\"left-pad@1.3.0\",\"status\":\"fail\"}",
			want: ansiRed + "progress {\n  \"package\": \"left-pad@1.3.0\",\n  \"status\": \"fail\"\n}" + ansiReset,
		},
		{
			name: "medium",
			msg:  "scan:vuln-json severity=medium provider=trivy finding={\"package\":\"left-pad@1.3.0\",\"status\":\"fail\"}",
			want: ansiYellow + "progress {\n  \"package\": \"left-pad@1.3.0\",\n  \"status\": \"fail\"\n}" + ansiReset,
		},
		{
			name: "low",
			msg:  "scan:vuln-json severity=low provider=trivy finding={\"package\":\"left-pad@1.3.0\",\"status\":\"fail\"}",
			want: ansiBlue + "progress {\n  \"package\": \"left-pad@1.3.0\",\n  \"status\": \"fail\"\n}" + ansiReset,
		},
		{
			name: "unknown",
			msg:  "scan:vuln-json severity=unknown provider=trivy finding={\"package\":\"left-pad@1.3.0\",\"status\":\"fail\"}",
			want: ansiMagenta + "progress {\n  \"package\": \"left-pad@1.3.0\",\n  \"status\": \"fail\"\n}" + ansiReset,
		},
		{
			name: "default missing severity",
			msg:  "scan:vuln-json provider=trivy finding={\"package\":\"left-pad@1.3.0\",\"status\":\"fail\"}",
			want: ansiRed + "progress {\n  \"package\": \"left-pad@1.3.0\",\n  \"status\": \"fail\"\n}" + ansiReset,
		},
		{
			name: "non vuln",
			msg:  "trivy:db:done elapsed=2s packages=1",
			want: "progress trivy:db:done elapsed=2s packages=1",
		},
		{
			name: "publish fail",
			msg:  "publish:fail processed=1/4 package=left-pad@1.3.0 error=boom",
			want: ansiRed + "progress publish:fail processed=1/4 package=left-pad@1.3.0 error=boom" + ansiReset,
		},
		{
			name: "exception",
			msg:  "scan:exception package=left-pad@1.3.0 severity=high ids=GHSA-123 provider=trivy reason=temporary_exception",
			want: ansiMagenta + "progress suppressed package=left-pad@1.3.0 severity=high ids=ghsa-123 provider=trivy reason=temporary_exception" + ansiReset,
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
