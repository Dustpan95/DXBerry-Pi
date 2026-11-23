#!/bin/bash
# Builds dxberryd and its private helpers beside it.
# Usage: station/build.sh [OUT [VERSION]]
# OUT defaults to provision/bin/dxberryd, VERSION to the contents of provision/VERSION (a release
# build passes the tag, so an rc suffix is in the binary).
set -uo pipefail
cd "$(dirname "$0")" || exit 1
out=${1:-../provision/bin/dxberryd}
version=${2:-$(cat ../provision/VERSION)} || exit 1
[[ -n $version ]] || exit 1
# nolibopusfile: hraban/opus leaves out its Ogg file streams (cgo pkg-config opusfile), so the binary
# links libopus only, the one Opus library the Pi installs (libopus0)
CGO_ENABLED=1 go build -tags nolibopusfile -trimpath -ldflags "-s -w -X main.version=$version" -o "$out" ./cmd/dxberryd || exit 1
CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o "$(dirname "$out")/dxberry-wsjtx-settings" ./cmd/dxberry-wsjtx-settings || exit 1
CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o "$(dirname "$out")/dxberry-preset" ./cmd/dxberry-preset || exit 1
CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o "$(dirname "$out")/dxberry-program-state" ./cmd/dxberry-program-state || exit 1
