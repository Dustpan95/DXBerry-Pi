#!/usr/bin/env python3
"""Check the one-key INI editor against Qt itself, using temporary files only."""
import pathlib
import subprocess
import sys
import tempfile

from PyQt5.QtCore import QSettings, qVersion


def edit(repo, file):
    return subprocess.run(
        ["bash", "-c", 'source "$1/common.sh"; source "$1/inifile.sh"; '
         'dxb_ini_set "$2" Configuration UDPServer 192.0.2.99',
         "ini-check", str(repo / "provision/lib"), str(file)],
        text=True, capture_output=True, check=False,
    )


def qt_host(file):
    settings = QSettings(str(file), QSettings.IniFormat)
    # Qt loads INI sections lazily. Full-dictionary readers (including program
    # profile operations) can resolve root-group aliases that a targeted lookup
    # alone has not loaded yet. Check the complete effective key namespace.
    settings.allKeys()
    result = settings.value("Configuration/UDPServer")
    assert settings.status() == QSettings.NoError
    return result


def main(repo):
    cases = {
        "duplicate-key": "[Configuration]\nUDPServer=first\nUDPServer=192.0.2.1\n",
        "duplicate-section": "[Configuration]\nUDPServer=first\n[Other]\nx=1\n"
                             "[Configuration]\nUDPServer=192.0.2.1\n",
        "percent-key": "[Configuration]\nUDP%53erver=192.0.2.1\n",
        "unicode-key": "[Configuration]\nUDP%U0053erver=192.0.2.1\n",
        "encoded-section": "[Con%66iguration]\nUDPServer=192.0.2.1\n",
        "root-group": "[General]\nConfiguration\\UDPServer=192.0.2.1\n",
        "whitespace-key": "[Configuration]\n UDPServer =192.0.2.1\n",
        "crlf": "[Configuration]\r\nUDPServer=192.0.2.1\r\n",
    }
    with tempfile.TemporaryDirectory() as directory:
        tmp = pathlib.Path(directory)
        for name, contents in cases.items():
            file = tmp / (name + ".ini")
            before = contents.encode("utf-8")
            file.write_bytes(before)
            actual = qt_host(file)
            assert actual == "192.0.2.1", (name, actual, before)
            result = edit(repo, file)
            assert result.returncode == 6, (name, result.returncode)
            assert file.read_bytes() == before, name
            assert qt_host(file) == "192.0.2.1", name
            print("ok", name)
        file = tmp / "canonical.ini"
        file.write_text('[Configuration]\nUDPServer="127.0.0.1"\nUDPServerPort=2237\n')
        result = edit(repo, file)
        assert result.returncode == 0, result.stderr
        assert qt_host(file) == "192.0.2.99"
        print("ok canonical-update")
        file = tmp / "real-first-run.ini"
        before = (repo / "tests/fixtures/apps/wsjtx.ini").read_bytes()
        file.write_bytes(before)
        result = edit(repo, file)
        assert result.returncode == 0, result.stderr
        assert file.read_bytes() == before.replace(b"UDPServer=127.0.0.1", b"UDPServer=192.0.2.99", 1)
        assert qt_host(file) == "192.0.2.99"
        print("ok real-first-run-update")
    print("10 Qt checks passed; Qt", qVersion())


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: qt-ini-check.py REPOSITORY")
    main(pathlib.Path(sys.argv[1]))
