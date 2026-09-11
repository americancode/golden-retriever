#!/usr/bin/env bash
set -euo pipefail

IMAGE_TAG="${1:-gr-ci:local}"

echo "Building ${IMAGE_TAG} from Dockerfile.ci..."
docker build -f Dockerfile.ci -t "${IMAGE_TAG}" .

REPORT_DIR="${RUNNER_TEMP:-${TMPDIR:-/tmp}}/golden-retriever-trivy"
mkdir -p "${REPORT_DIR}"

echo "Scanning ${IMAGE_TAG} with Trivy (all severities)..."
trivy image \
  --severity UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL \
  --skip-dirs /usr/local/lib/node_modules/npm \
  --format json \
  --output "${REPORT_DIR}/trivy-image.json" \
  --exit-code 0 \
  "${IMAGE_TAG}"

trivy convert --format sarif \
  --output "${REPORT_DIR}/trivy-image.sarif" "${REPORT_DIR}/trivy-image.json"

critical="$(jq '[.Results[]?.Vulnerabilities[]? | select(.Severity == "CRITICAL")] | length' "${REPORT_DIR}/trivy-image.json")"
high="$(jq '[.Results[]?.Vulnerabilities[]? | select(.Severity == "HIGH")] | length' "${REPORT_DIR}/trivy-image.json")"
echo "Image vulnerabilities: CRITICAL=${critical} HIGH=${high}"
(( critical == 0 && high <= 15 ))

echo "Scan passed: ${IMAGE_TAG}"
