#!/usr/bin/env bash
set -euo pipefail

IMAGE_TAG="${1:-gr-ci:local}"

echo "Building ${IMAGE_TAG} from Dockerfile.ci..."
docker build -f Dockerfile.ci -t "${IMAGE_TAG}" .

echo "Scanning ${IMAGE_TAG} with Trivy (HIGH,CRITICAL)..."
trivy image --severity HIGH,CRITICAL --ignore-unfixed --exit-code 1 "${IMAGE_TAG}"

echo "Scan passed: ${IMAGE_TAG}"
