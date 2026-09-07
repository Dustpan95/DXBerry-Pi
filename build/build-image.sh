#!/usr/bin/env bash
# Build a DXBerry-Pi image: official DietPi image + /opt/dxberry + first-boot hooks. Needs root for loop mounts.
set -uo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
DIETPI_URL=${DIETPI_URL:-https://dietpi.com/downloads/images/DietPi_RPi234-ARMv8-Trixie.img.xz}
WORK=$ROOT/build/work
OUT=$ROOT/out
VERSION=''
DIETPI_IMG=''
CHECK=0
KEEP=0
MNT=''
LOOP=''

usage() {
  cat << 'EOF'
usage: build/build-image.sh [--version V] [--dietpi-image PATH] [--check] [--keep-work]
  --version V         image version (default: git describe)
  --dietpi-image P    use an already downloaded DietPi .img.xz instead of downloading
  --check             verify the repository tree only (no root, no network)
  --keep-work         keep build/work after a successful build
EOF
}

die() {
  echo "$*" >&2
  exit 1
}

while (( $# )); do
  case $1 in
    --version)
      if (( $# < 2 )); then echo "--version requires a value" >&2; usage >&2; exit 2; fi
      VERSION=$2
      shift
      ;;
    --dietpi-image)
      if (( $# < 2 )); then echo "--dietpi-image requires a value" >&2; usage >&2; exit 2; fi
      DIETPI_IMG=$2
      shift
      ;;
    --check) CHECK=1 ;;
    --keep-work) KEEP=1 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

REQUIRED_FILES=(
  boot/dietpi.overrides.txt boot/dxberry.txt.example boot/README-DXBERRY.txt
  boot/Automation_Custom_PreScript.sh boot/Automation_Custom_Script.sh
  provision/VERSION provision/bin/dxberry-preboot provision/bin/dxberry-provision provision/bin/dxberry-netwatch
  provision/lib/common.sh provision/lib/config.sh provision/lib/system.sh provision/lib/network.sh
  provision/lib/storage.sh provision/lib/graywolf.sh provision/lib/scrub.sh
  provision/templates/interfaces-eth0.tmpl provision/templates/interfaces-wlan0.tmpl
  provision/templates/journald-dxberry.conf provision/templates/dxberry-netwatch.service
)

check_tree() {
  local f ok=1
  for f in "${REQUIRED_FILES[@]}"; do
    if [[ ! -f $ROOT/$f ]]; then
      echo "missing $f" >&2
      ok=0
    fi
  done
  for f in "$ROOT"/provision/bin/* "$ROOT"/provision/lib/*.sh "$ROOT"/boot/*.sh; do
    if [[ -f $f ]] && ! bash -n "$f"; then
      echo "syntax error in ${f#"$ROOT"/}" >&2
      ok=0
    fi
  done
  if (( ! ok )); then return 1; fi
  echo "tree ok"
}

if (( CHECK )); then
  check_tree
  exit $?
fi

if ! check_tree > /dev/null; then
  exit 1
fi
if (( EUID != 0 )); then
  echo "the build must run as root (sudo) for loop-device mounts" >&2
  exit 1
fi
for t in losetup xz sha256sum curl partprobe mount; do
  if ! command -v "$t" > /dev/null; then
    echo "missing tool: $t" >&2
    exit 1
  fi
done
if [[ -n $DIETPI_IMG ]] && [[ ! -f $DIETPI_IMG ]]; then
  die "dietpi image not found: $DIETPI_IMG"
fi
# shellcheck source=provision/lib/common.sh
if ! source "$ROOT/provision/lib/common.sh"; then
  die "failed to source provision/lib/common.sh"
fi

VERSION=${VERSION:-$(git -C "$ROOT" describe --tags --always --dirty 2> /dev/null || echo dev)}
VERSION=${VERSION#v}
if ! mkdir -p "$WORK" "$OUT"; then
  die "failed to create $WORK or $OUT"
fi

cleanup() {
  if [[ -n $MNT ]]; then
    umount "$MNT/boot" 2> /dev/null
    umount "$MNT/root" 2> /dev/null
    rmdir "$MNT/boot" "$MNT/root" "$MNT" 2> /dev/null
  fi
  if [[ -n $LOOP ]]; then
    losetup -d "$LOOP" 2> /dev/null
  fi
  return 0
}
trap cleanup EXIT

# 1. Official DietPi image, verified against DietPi's published checksum.
name=$(basename "$DIETPI_URL")
if [[ -z $DIETPI_IMG ]]; then
  DIETPI_IMG=$WORK/$name
  if ! curl -fsSL -o "$WORK/$name.sha256" "$DIETPI_URL.sha256"; then
    die "failed to download $DIETPI_URL.sha256"
  fi
  if [[ ! -f $DIETPI_IMG ]] || ! (cd "$WORK" && sha256sum -c --quiet "$name.sha256"); then
    echo "downloading $DIETPI_URL"
    if ! curl -fL -o "$DIETPI_IMG" "$DIETPI_URL"; then
      die "failed to download $DIETPI_URL"
    fi
    if ! (cd "$WORK" && sha256sum -c --quiet "$name.sha256"); then
      die "checksum verification failed for $DIETPI_IMG"
    fi
  fi
fi
dietpi_sha=$(sha256sum "$DIETPI_IMG" | cut -d' ' -f1)
if [[ -z $dietpi_sha ]]; then
  die "failed to hash $DIETPI_IMG"
fi

# 2. Decompress and expose partitions.
img=$WORK/DXBerry-Pi-$VERSION-rpi234-arm64.img
echo "decompressing to $img"
if ! xz -dkc "$DIETPI_IMG" > "$img"; then
  die "failed to decompress $DIETPI_IMG"
fi
LOOP=$(losetup -Pf --show "$img")
if [[ -z $LOOP ]]; then
  die "losetup failed for $img"
fi
if ! partprobe "$LOOP"; then
  die "partprobe failed for $LOOP"
fi
MNT=$(mktemp -d)
if [[ -z $MNT ]]; then
  die "mktemp -d failed"
fi
if ! mkdir -p "$MNT/boot" "$MNT/root"; then
  die "failed to create mount points under $MNT"
fi
if ! mount "${LOOP}p1" "$MNT/boot"; then
  die "failed to mount ${LOOP}p1"
fi
if ! mount "${LOOP}p2" "$MNT/root"; then
  die "failed to mount ${LOOP}p2"
fi

# 3. DietPi automation defaults: the root copy is authoritative, the FAT copy is what users see.
#    DietPi imports the FAT copy at first boot only if it is newer (cp -u), so stamp it older.
apply_overrides() {
  local target=$1 line
  while IFS= read -r line; do
    if [[ -z $line || $line == \#* ]]; then continue; fi
    if ! dxb_set_kv "$target" "${line%%=*}" "${line#*=}"; then
      die "failed to apply override: $line"
    fi
  done < "$ROOT/boot/dietpi.overrides.txt"
}
apply_overrides "$MNT/root/boot/dietpi.txt"
if ! cp "$MNT/root/boot/dietpi.txt" "$MNT/boot/dietpi.txt"; then
  die "failed to copy dietpi.txt to the FAT partition"
fi
if ! touch -d '1970-01-01 00:00:00 UTC' "$MNT/boot/dietpi.txt"; then
  die "failed to stamp the FAT dietpi.txt"
fi
if ! touch -d '1970-01-01 00:01:00 UTC' "$MNT/root/boot/dietpi.txt"; then
  die "failed to stamp the root dietpi.txt"
fi
for s in Automation_Custom_PreScript.sh Automation_Custom_Script.sh; do
  if ! install -m 755 -o 0 -g 0 "$ROOT/boot/$s" "$MNT/root/boot/$s"; then
    die "failed to install $s"
  fi
done
if ! cp "$ROOT/boot/dxberry.txt.example" "$ROOT/boot/README-DXBERRY.txt" "$MNT/boot/"; then
  die "failed to copy the boot README and example config to the FAT partition"
fi

# 4. The provisioner.
if ! rm -rf "$MNT/root/opt/dxberry"; then
  die "failed to clear /opt/dxberry"
fi
if ! mkdir -p "$MNT/root/opt/dxberry"; then
  die "failed to create /opt/dxberry"
fi
if ! cp -r "$ROOT/provision/." "$MNT/root/opt/dxberry/"; then
  die "failed to copy the provisioner into /opt/dxberry"
fi
if ! chown -R 0:0 "$MNT/root/opt/dxberry"; then
  die "failed to chown /opt/dxberry"
fi
if ! find "$MNT/root/opt/dxberry" -type d -exec chmod 755 {} +; then
  die "failed to chmod directories under /opt/dxberry"
fi
if ! find "$MNT/root/opt/dxberry" -type f -exec chmod 644 {} +; then
  die "failed to chmod files under /opt/dxberry"
fi
if ! chmod 755 "$MNT/root/opt/dxberry/bin/"*; then
  die "failed to chmod /opt/dxberry/bin"
fi
if ! mkdir -p "$MNT/root/usr/local/sbin"; then
  die "failed to create /usr/local/sbin"
fi
if ! ln -sf /opt/dxberry/bin/dxberry-provision "$MNT/root/usr/local/sbin/dxberry-provision"; then
  die "failed to symlink dxberry-provision"
fi
if ! ln -sf /opt/dxberry/bin/dxberry-netwatch "$MNT/root/usr/local/sbin/dxberry-netwatch"; then
  die "failed to symlink dxberry-netwatch"
fi
if ! cat > "$MNT/root/etc/dxberry-release" << EOF
DXBERRY_VERSION=$VERSION
DXBERRY_COMMIT=$(git -C "$ROOT" rev-parse --short HEAD 2> /dev/null || echo unknown)
DXBERRY_BUILD_DATE=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
DIETPI_IMAGE=$name
DIETPI_IMAGE_SHA256=$dietpi_sha
EOF
then
  die "failed to write /etc/dxberry-release"
fi

# 5. Unmount, compress, checksum.
sync
if ! umount "$MNT/boot"; then
  die "failed to unmount the FAT partition"
fi
if ! umount "$MNT/root"; then
  die "failed to unmount the root partition"
fi
if ! rmdir "$MNT/boot" "$MNT/root" "$MNT"; then
  die "failed to remove mount points"
fi
MNT=''
if ! losetup -d "$LOOP"; then
  die "failed to detach $LOOP"
fi
LOOP=''
final="$OUT/$(basename "$img")"
if ! mv "$img" "$final"; then
  die "failed to move the image to $final"
fi
echo "compressing $final"
if ! xz -T0 -f "$final"; then
  die "failed to compress $final"
fi
if ! (cd "$OUT" && sha256sum "$(basename "$final").xz" > "$(basename "$final").xz.sha256"); then
  die "failed to checksum $final.xz"
fi
if (( ! KEEP )); then
  rm -f "$WORK/DXBerry-Pi-"*.img
fi
echo "built $final.xz"
echo "sha256: $(cut -d' ' -f1 "$final.xz.sha256")"
