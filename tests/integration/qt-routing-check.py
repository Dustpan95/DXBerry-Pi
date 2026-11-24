#!/usr/bin/env python3
"""Read actual Go settings transactions with independent Qt 5 QSettings."""
import json
import os
import pathlib
import subprocess
import sys
import tempfile

from PyQt5.QtCore import QSettings, qVersion


def qt(file):
    settings = QSettings(str(file), QSettings.IniFormat)
    settings.allKeys()  # Match upstream's complete profile enumeration.
    assert settings.status() == QSettings.NoError, file.name
    return settings


def main(repo, binary):
    base = ("[Configuration]\nUDPServer=localhost\nUDPServerPort=2237\n"
            "AcceptUDPRequests=false\nN1MMServerPort=2333\n"
            "[MultiSettings]\nCurrentName=Default\n")
    valid = {
        "canonical": base,
        "absent": "[Configuration]\nOther=keep\n[MultiSettings]\nCurrentName=Default\n",
        "empty": base.replace("UDPServer=localhost", "UDPServer=")
                     .replace("UDPServerPort=2237", "UDPServerPort="),
        "quoted-host": base.replace("UDPServer=localhost", 'UDPServer="localhost"')
                           .replace("UDPServerPort=2237", "UDPServerPort=02237"),
        "saved-profiles": base + "Other%20Profile\\Configuration\\UDPServer=192.0.2.1\n"
                                 "Other%20Profile\\Configuration\\UDPServerPort=2334\n"
                                 "Empty\\MultiSettingsPlaceHolder=@Invalid()\n",
        "literal-general": "[General]\nkeep=root\n[%General]\nkeep=literal\n" + base,
        "real-first-run": (repo / "tests/fixtures/apps/wsjtx.ini").read_text(),
    }
    # Each refusal has a specific Qt interpretation, demonstrating why it
    # must not be treated as a missing/simple key by the canonical editor.
    refused = {
        "duplicate-key": (base.replace("UDPServer=localhost", "UDPServer=first\nUDPServer=192.0.2.10"),
                          "Configuration/UDPServer", "192.0.2.10"),
        "duplicate-section": (base + "[Configuration]\nUDPServer=192.0.2.10\n",
                              "Configuration/UDPServer", "192.0.2.10"),
        "percent-key": (base.replace("UDPServer=localhost", "UDP%53erver=192.0.2.10"),
                        "Configuration/UDPServer", "192.0.2.10"),
        "unicode-key": (base.replace("UDPServer=localhost", "UDP%U0053erver=192.0.2.10"),
                        "Configuration/UDPServer", "192.0.2.10"),
        "section-alias": (base.replace("[Configuration]", "[Con%66iguration]")
                          .replace("UDPServer=localhost", "UDPServer=192.0.2.10"),
                          "Configuration/UDPServer", "192.0.2.10"),
        "root-group": ("[General]\nConfiguration\\UDPServer=192.0.2.10\n" + base.replace("UDPServer=localhost\n", ""),
                       "Configuration/UDPServer", "192.0.2.10"),
        "saved-key-alias": (base + "Other\\Configuration\\UDP%53erver=192.0.2.10\n",
                            "MultiSettings/Other/Configuration/UDPServer", "192.0.2.10"),
        "saved-root-alias": ("[General]\nMultiSettings\\Other\\Configuration\\UDPServer=192.0.2.10\n" + base,
                             "MultiSettings/Other/Configuration/UDPServer", "192.0.2.10"),
        "whitespace": (base.replace("UDPServer=localhost", " UDPServer =192.0.2.10"),
                       "Configuration/UDPServer", "192.0.2.10"),
        "crlf": (base.replace("\n", "\r\n"), "Configuration/UDPServer", "localhost"),
        "quoted-profile": (base.replace("CurrentName=Default", 'CurrentName="Default"'),
                           "MultiSettings/CurrentName", "Default"),
        "no-final-newline": (base.rstrip("\n"), "Configuration/UDPServer", "localhost"),
        "continued-value": (base.replace("UDPServer=localhost", "Other=first\\\nUDPServer=hidden"),
                            "Configuration/UDPServer", None),
        "multiline-quote": (base.replace("UDPServer=localhost", 'Other="first\nUDPServer=hidden\nend="'),
                            "Configuration/UDPServer", None),
    }
    with tempfile.TemporaryDirectory() as directory:
        tmp = pathlib.Path(directory)
        for name, contents in valid.items():
            file = tmp / (name + ".ini")
            original = contents.encode()
            file.write_bytes(original)
            before = qt(file)
            before.beginGroup("MultiSettings")
            saved = sorted(before.childGroups())
            before.endGroup()
            result_path = tmp / "result.json"
            env = dict(os.environ, DXB_QT_SETTINGS_CASE=str(file), DXB_QT_SETTINGS_RESULT=str(result_path))
            run = subprocess.run([str(binary), "-test.run=^TestSettingsQtReferenceHelper$"],
                                 env=env, text=True, capture_output=True, check=False)
            assert run.returncode == 0, (name, run.stdout, run.stderr)
            result = json.loads(result_path.read_text())
            assert result["Accepted"] and result["SavedProfiles"] == saved, (name, result, saved)
            assert result["Profile"] == before.value("MultiSettings/CurrentName"), name
            managed = qt(pathlib.Path(str(file) + ".managed"))
            assert managed.value("Configuration/UDPServer") == "127.0.0.1", name
            assert int(managed.value("Configuration/UDPServerPort")) == result["Port"], name
            assert managed.value("Configuration/AcceptUDPRequests") == before.value("Configuration/AcceptUDPRequests"), name
            assert managed.value("Configuration/N1MMServerPort") == before.value("Configuration/N1MMServerPort"), name
            for profile in saved:
                for key in ("UDPServer", "UDPServerPort"):
                    path = f"MultiSettings/{profile}/Configuration/{key}"
                    assert managed.value(path) == before.value(path), (name, path)
            assert pathlib.Path(str(file) + ".restored").read_bytes() == original, name
            assert file.read_bytes() == original, name
            qt(pathlib.Path(str(file) + ".restored"))
            print("ok transaction", name)
        for name, (contents, key, expected) in refused.items():
            file = tmp / (name + ".ini")
            original = contents.encode()
            file.write_bytes(original)
            assert qt(file).value(key) == expected, (name, qt(file).value(key), expected)
            result_path = tmp / "result.json"
            env = dict(os.environ, DXB_QT_SETTINGS_CASE=str(file), DXB_QT_SETTINGS_RESULT=str(result_path))
            run = subprocess.run([str(binary), "-test.run=^TestSettingsQtReferenceHelper$"],
                                 env=env, text=True, capture_output=True, check=False)
            assert run.returncode == 0, (name, run.stdout, run.stderr)
            assert not json.loads(result_path.read_text())["Accepted"], name
            assert file.read_bytes() == original, name
            print("ok refusal", name)
    print(f"{len(valid) + len(refused)} Qt routing checks passed; Qt {qVersion()}")


if __name__ == "__main__":
    if len(sys.argv) != 3:
        raise SystemExit("usage: qt-routing-check.py REPOSITORY GO_TEST_BINARY")
    main(pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2]))
