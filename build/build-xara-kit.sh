#!/bin/bash
# Build the pinned native XARA kit on an ARM64 Linux host.
set -euo pipefail
revision=d00900ab771c80f27c0a0b60664eff256ed32022
source_sha=0408d52f2a7ba5544bb226f0236cb36038650b282468066b0c625c129f8b430a
root=$(cd "$(dirname "$0")/.." && pwd)
out=$(realpath -m "${1:-$root/provision/share/xara.zip}")
[[ $(uname -m) == aarch64 ]] || { echo "XARA kit requires an ARM64 Linux builder" >&2; exit 1; }
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
curl --proto '=https' --proto-redir '=https' -fsSL --retry 3 \
  "https://codeload.github.com/Dustpan95/xara/tar.gz/$revision" -o "$work/source.tar.gz"
echo "$source_sha  $work/source.tar.gz" | sha256sum -c
tar -xzf "$work/source.tar.gz" -C "$work" --strip-components=1
cargo test --locked --manifest-path "$work/Cargo.toml" -p xara --features varac-17 --test host_protocol
cargo build --locked --manifest-path "$work/Cargo.toml" --release -p xara --bin xara --features varac-17
mkdir -p "$(dirname "$out")"
python3 - "$work" "$out" "0.1.3+$revision" <<'PY'
import sys
from pathlib import Path
from zipfile import ZIP_DEFLATED, ZipFile

source, output, version = sys.argv[1:]
source = Path(source)
with ZipFile(output, "w", ZIP_DEFLATED) as kit:
    kit.write(source / "target/release/xara", "xara")
    kit.write(source / "LICENSE", "LICENSE")
    kit.writestr("VERSION", version + "\n")
PY
python3 "$root/provision/lib/native-archive.py" xara "$out"
