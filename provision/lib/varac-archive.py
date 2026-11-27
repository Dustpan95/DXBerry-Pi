"""Inspect or stage the publisher's native ARM64 VarAC kit."""
import re
import shutil
import sys
import zipfile
from pathlib import Path

FILES = ("VarAC", "README.txt", "License.txt", "THIRD-PARTY-LICENSES.txt",
         "VarAC_cat_commands.ini", "VarAC_UI_languages.ini", "VarAC_templates.ini")


def read_kit(archive, destination=None):
    with zipfile.ZipFile(archive) as kit:
        entries = kit.namelist()
        for name in FILES:
            member = "VarAC/" + name
            if entries.count(member) != 1 or kit.getinfo(member).file_size > 256 * 1024**2:
                raise ValueError("incomplete or unsupported VarAC kit")
        with kit.open("VarAC/VarAC") as binary:
            elf = binary.read(20)
        if elf[:6] != b"\x7fELF\x02\x01" or elf[18:20] != b"\xb7\x00":
            raise ValueError("the VarAC executable must be 64-bit ARM Linux")
        with kit.open("VarAC/README.txt") as readme:
            version = re.match(rb"VarAC (\d+(?:\.\d+){2,3})\b", readme.read(256))
        if not version:
            raise ValueError("missing VarAC version in the publisher README")
        if destination:
            for name in FILES:
                path = Path(destination, name)
                with kit.open("VarAC/" + name) as source, path.open("xb") as target:
                    shutil.copyfileobj(source, target)
                path.chmod(0o755 if name == "VarAC" else 0o644)
        return version[1].decode("ascii")


if __name__ == "__main__":
    try:
        print(read_kit(*sys.argv[1:]))
    except (OSError, ValueError, RuntimeError, zipfile.BadZipFile) as error:
        sys.exit(f"VarAC kit: {error}")
