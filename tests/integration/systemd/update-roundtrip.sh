#!/bin/bash
# Runs only inside the disposable no-NIC guest after the independent queue test.
set -Eeuo pipefail
trap 'echo "Update round trip failed at line $LINENO: $BASH_COMMAND" >&2; journalctl -q --no-pager -n 12 -u dxberryd.service >&2' ERR
[[ $(hostname) == dxb-ci-guest && $(< /proc/1/comm) == systemd ]]
[[ $(cat /etc/machine-id) == 11223344556677889900aabbccddeeff ]]
[[ ! -e /opt/dxberry.prev && ! -e /run/dxberry/update-job.json ]]
# A kernel can expose the ALSA timer or sequencer without a sound card.
shopt -s nullglob
cards=(/sys/class/sound/card[0-9]* /dev/snd/controlC* /dev/snd/pcmC* /dev/snd/midiC* /dev/ttyUSB* /dev/ttyACM*)
printf 'Guest sound entries: %s\n' /dev/snd/*
(( ${#cards[@]} == 0 ))
# The queue driver exports a private run directory for its renamed units.
# Public admission, transient workers and result readers use the station default.
unset DXB_RUN_DIR
root=/opt/dxberry
state=/var/lib/dxberry
evidence=/run/dxb-ci
packages=/fixture/packages
name=dxberry-pi-0.0.2.tar.gz
update=$root/bin/dxberry-update
[[ $(< "$root/VERSION") == 0.0.1 ]]
(cd "$packages" && sha256sum -c "$name.sha256")
jq -n --arg name "$name" --arg url "file://$packages/$name" \
  '[{tag_name:"v0.0.2",draft:false,prerelease:false,assets:[
     {name:$name,browser_download_url:$url},{name:($name+".sha256"),browser_download_url:($url+".sha256")}]}]' \
  > "$packages/releases.json"

mkdir -p "$state/station" /etc/dxberry /boot
chmod 755 "$state/station"
printf '%s\n' 'PASSWORD=<applied>' > /boot/dxberry.txt
printf '%s\n' '{"version":1,"radios":{},"gps":{"device":"none","baud":9600,"pps":""}}' > "$state/radios.json"
sha256sum /boot/dxberry.txt "$state/radios.json" > "$evidence/operator.sha256"
cp "$root/RELEASE" /etc/dxberry-release
echo 'DIETPI_IMAGE=fixture' >> /etc/dxberry-release
"$root/bin/dxberry-program-state" prepare-advance
epoch=$state/station/program-state/epoch
original_epoch=$(sha256sum < "$epoch")
original_tree=$(stat -c '%d:%i' "$root")
(cd "$root" && find . -type f -print0 | sort -z | xargs -0 sha256sum) > "$evidence/original.sha256"
install -m 644 "$root/templates/dxberryd.service" /etc/systemd/system/dxberryd.service
systemctl daemon-reload
ip link set lo up
systemctl start dxberryd.service

check_console() {
  local version=$1 pid i
  for ((i=0; i<60; i++)); do
    if curl -fsS --max-time 1 http://127.0.0.1:8090/api/version 2>/dev/null |
      jq -e --arg version "$version" '.version == $version' >/dev/null; then break; fi
    sleep .25
  done
  (( i < 60 ))
  pid=$(systemctl show dxberryd.service -p MainPID --value)
  [[ $pid -gt 1 && /proc/$pid/exe -ef $root/bin/dxberryd ]]
  [[ $(< "$root/VERSION") == "$version" ]]
  grep -Fxq "DXBERRY_VERSION=$version" /etc/dxberry-release
  grep -Fxq 'DIETPI_IMAGE=fixture' /etc/dxberry-release
  sha256sum -c "$evidence/operator.sha256"
  [[ ! -e $state/station/wsjtx && ! -e /run/dxberry/radio-events ]]
}

run_job() {
  local kind=$1 version=$2 response unit report i
  response=$("$update" "$kind" --json)
  unit=$(jq -er --arg k "$kind" 'select(.ok and .kind==$k) | .job' <<< "$response")
  # The worker exchanges the tree containing the public CLI. Wait through
  # that interval using the manager that owns the admitted transient unit.
  for ((i=0; i<60; i++)); do
    systemctl is-active --quiet "$unit" || break
    sleep .5
  done
  if (( i == 60 )); then
    journalctl --no-pager -u "$unit"
    echo "Timed out waiting for $unit" >&2
    return 1
  fi
  report=$("$update" job --json)
  if ! jq -e --arg u "$unit" '.job.unit==$u and .job.state=="finished" and .job.exit==0' <<< "$report" >/dev/null; then
    journalctl --no-pager -u "$unit"
    echo "$report" >&2
    return 1
  fi
  grep -Fxq "$unit $version" "$evidence/setup-history"
  check_console "$version"
  echo "PASS public $kind job finished and served $version"
}

check_console 0.0.1
systemctl set-environment "DXB_UPDATE_API=file://$packages/releases.json" DXB_UPDATE_PROVISION=/fixture/check-update-setup
run_job dxberry 0.0.2
candidate_tree=$(stat -c '%d:%i' "$root")
[[ $candidate_tree != "$original_tree" && $(stat -c '%d:%i' "$root.prev") == "$original_tree" ]]
(cd "$root.prev" && sha256sum --quiet -c "$evidence/original.sha256")
tar -dzf "$packages/$name" -C /opt
(cd "$root" && find . -type f -print0 | sort -z | xargs -0 sha256sum) > "$evidence/candidate.sha256"
candidate_epoch=$(sha256sum < "$epoch")
[[ $candidate_epoch != "$original_epoch" && /usr/local/sbin/dxberry-update -ef $update ]]
run_job rollback 0.0.1
[[ $(stat -c '%d:%i' "$root") == "$original_tree" && $(stat -c '%d:%i' "$root.prev") == "$candidate_tree" ]]
(cd "$root" && sha256sum --quiet -c "$evidence/original.sha256")
(cd "$root.prev" && sha256sum --quiet -c "$evidence/candidate.sha256")
[[ $(sha256sum < "$epoch") != "$candidate_epoch" && $(wc -l < "$evidence/setup-history") == 2 ]]
[[ ! -e $root.next && ! -e $root.new && ! -e $root.old && /usr/local/sbin/dxberry-update -ef $update ]]
systemctl unset-environment DXB_UPDATE_API DXB_UPDATE_PROVISION
systemctl stop dxberryd.service
echo 'PASS original tree restored, candidate retained, settings preserved through both exchanges'
