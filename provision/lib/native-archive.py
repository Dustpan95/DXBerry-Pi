"""Inspect or stage a known native ARM64 program kit without running it."""
import re
import shutil
import sys
import zipfile
from pathlib import Path

VARAC_FILES = ("VarAC", "README.txt", "License.txt", "THIRD-PARTY-LICENSES.txt",
         "VarAC_cat_commands.ini", "VarAC_UI_languages.ini", "VarAC_templates.ini")


def read_kit(program, archive, destination=None):
    if program == "varac":
        files, prefix, exe, metadata = VARAC_FILES, "VarAC/", "VarAC", "README.txt"
    elif program == "xara":
        files, prefix, exe, metadata = ("xara", "LICENSE", "VERSION"), "", "xara", "VERSION"
    else:
        raise ValueError("unknown program")
    with zipfile.ZipFile(archive) as kit:
        entries = kit.namelist()
        for name in files:
            member = prefix + name
            if entries.count(member) != 1 or kit.getinfo(member).file_size > 256 * 1024**2:
                raise ValueError("incomplete or unsupported program kit")
        with kit.open(prefix + exe) as binary:
            elf = binary.read(20)
        if elf[:6] != b"\x7fELF\x02\x01" or elf[18:20] != b"\xb7\x00":
            raise ValueError("the executable must be 64-bit ARM Linux")
        with kit.open(prefix + metadata) as readme:
            header = readme.read(256)
            if program == "varac":
                version = re.match(rb"VarAC (\d+(?:\.\d+){2,3})\b", header)
            else:
                version = re.fullmatch(rb"(\d+\.\d+\.\d+(?:\+[a-f0-9]{40})?)\s*", header) if not readme.read(1) else None
        if not version:
            raise ValueError("missing program version")
        if destination:
            for name in files:
                path = Path(destination, name)
                with kit.open(prefix + name) as source, path.open("xb") as target:
                    shutil.copyfileobj(source, target)
                path.chmod(0o755 if name == exe else 0o644)
        return version[1].decode("ascii")


if __name__ == "__main__":
    try:
        print(read_kit(*sys.argv[1:]))
    except (OSError, ValueError, RuntimeError, zipfile.BadZipFile) as error:
        sys.exit(f"Program kit: {error}")
