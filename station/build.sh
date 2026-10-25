#!/bin/bash
# Builds dxberryd. Usage: station/build.sh [OUT [VERSION]]
# OUT defaults to provision/bin/dxberryd, VERSION to the contents of provision/VERSION (a release
# build passes the tag, so an rc suffix is in the binary).
set -uo pipefail
cd "$(dirname "$0")" || exit 1
out=${1:-../provision/bin/dxberryd}
version=${2:-$(cat ../provision/VERSION)} || exit 1
[[ -n $version ]] || exit 1
CGO_ENABLED=1 go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$out" ./cmd/dxberryd
