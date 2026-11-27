"""FT-991 power control for dxberry-radio after validation of the USB CAT pin."""
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import time


def ctl(*args):
    return subprocess.check_output(["systemctl", *args], text=True, timeout=10).strip()


def command(stream, name):
    stream.write(("+\\" + name + "\n").encode("ascii"))
    fields = {}
    for _ in range(16):
        line = stream.readline(1024).decode("ascii").strip()
        if line.startswith("RPRT "):
            if line != "RPRT 0":
                raise ValueError(f"Hamlib rejected {name} ({line})")
            return fields
        if not line:
            break
        key, _, value = line.partition(":")
        fields[key] = value.strip()
    raise OSError("No complete reply from the radio controller")


def live_power(stream, action):
    if action == "off":
        if command(stream, "get_ptt").get("PTT") != "0":
            raise ValueError("End the transmission before powering off the radio")
        command(stream, "set_powerstat 0")
    elif action == "on":
        command(stream, "set_powerstat 1")
    # The UART can remain present while the power transition is in progress.
    # A fresh read after settling is stronger evidence than the write acknowledgement.
    if action != "status":
        time.sleep(2)
    state = "unknown"
    for attempt in range(3 if action != "status" else 1):
        try:
            reply = command(stream, "get_powerstat").get("Power Status")
            state = {"0": "off", "1": "on"}.get(reply, "unknown")
        except ValueError:
            state = "unknown"
        except OSError:
            return "unknown"
        if state == action or action == "status":
            break
        if attempt < 2:
            time.sleep(1)
    return state


def holds_device(pid, device):
    try:
        fds = list(Path(f"/proc/{pid}/fd").iterdir())
    except FileNotFoundError:
        return False
    for fd in fds:
        try:
            if os.path.samefile(fd, device):
                return True
        except FileNotFoundError:
            continue
    return False


def power(name, action, device, port, baud):
    unit = f"rigctld@{name}.service"
    pid = int(ctl("show", unit, "-p", "MainPID", "--value"))
    if pid:
        if not holds_device(pid, device):
            raise ValueError("Radio controller is starting or no longer owns the pinned USB device")
        with socket.create_connection(("127.0.0.1", int(port)), timeout=3) as sock:
            sock.settimeout(20)
            # Match this TCP connection to the service holding the USB device.
            peers = subprocess.check_output(["ss", "-Hntp", "sport", "=", ":" + port,
                                             "and", "dport", "=", ":" + str(sock.getsockname()[1])],
                                            text=True, timeout=3)
            if f"pid={pid}," not in peers or not holds_device(pid, device):
                raise ValueError("Unable to verify the radio controller for this connection")
            with sock.makefile("rwb", buffering=0) as stream:
                return live_power(stream, action)
    if action != "on":
        raise ValueError("USB CAT controller unavailable; radio power state is unknown")
    # A cold Pi may have no rigctld because the transceiver was already off.
    # Stop pending restarts while the one-shot Hamlib client owns the UART.
    ctl("stop", unit)
    try:
        if any(holds_device(int(p.name), device) for p in Path("/proc").iterdir()
               if p.name.isdigit() and (p / "fd").is_dir()):
            raise ValueError("USB CAT port is in use")
        result = subprocess.run(["rigctl", "-m", "1035", "-r", device, "-s", baud,
                                 "-C", "auto_power_on=1", "get_powerstat"],
                                capture_output=True, text=True, timeout=25)
        if result.returncode or result.stdout.strip().splitlines()[-1:] != ["1"]:
            raise ValueError("FT-991 did not confirm power on. Check its DC supply and USB CAT cable")
        return "on"
    finally:
        ctl("--no-block", "start", unit)


if __name__ == "__main__":
    try:
        print(json.dumps({"state": power(*sys.argv[1:])}))
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        sys.exit(str(error))
