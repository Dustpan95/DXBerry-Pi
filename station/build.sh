#!/bin/bash
# Builds dxberryd. Usage: station/build.sh [OUT]   (default: provision/bin/dxberryd)
set -uo pipefail
cd "$(dirname "$0")" || exit 1
out=${1:-../provision/bin/dxberryd}
version=$(cat ../provision/VERSION) || exit 1
CGO_ENABLED=1 go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$out" ./cmd/dxberryd
