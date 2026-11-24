"""Pack a private guest initramfs without creating host device nodes."""
from pathlib import Path
import gzip, os, shutil, stat, sys
root, dest = map(Path, sys.argv[1:])
kernels = list((root/'boot').glob('vmlinuz-*'))
if len(kernels) != 1:
    raise SystemExit('expected one distribution kernel')
shutil.copyfile(kernels[0], dest/'vmlinuz')
exclude = {'proc', 'sys', 'dev', 'run', 'tmp', 'boot', 'root', '.dockerenv', '.containerenv'}
ino = 0
payload_bytes = 0
with gzip.open(dest/'root.cpio.gz', 'wb', compresslevel=1) as out:
    def emit(name, mode, data=b'', uid=0, gid=0, major=0, minor=0):
        global ino, payload_bytes
        ino += 1
        payload_bytes += len(data)
        encoded = name.encode() + b'\0'
        values = [ino, mode, uid, gid, 1, 0, len(data), 0, 0, major, minor, len(encoded), 0]
        header = b'070701' + b''.join(f'{v:08x}'.encode() for v in values)
        out.write(header + encoded)
        out.write(b'\0' * (-(len(header)+len(encoded)) % 4))
        out.write(data)
        out.write(b'\0' * (-len(data) % 4))
    for current, dirs, files in os.walk(root, followlinks=False):
        rel = Path(current).relative_to(root)
        if rel == Path('.'):
            dirs[:] = sorted(d for d in dirs if d not in exclude)
            files = [f for f in files if f not in exclude]
        for name in sorted([*dirs, *files]):
            path = Path(current)/name
            relative = str(path.relative_to(root))
            info = path.lstat()
            if relative in {'etc/hostname', 'etc/hosts', 'etc/resolv.conf'}:
                continue
            if stat.S_ISLNK(info.st_mode):
                data = os.readlink(path).encode()
            elif stat.S_ISREG(info.st_mode):
                data = path.read_bytes()
            elif stat.S_ISDIR(info.st_mode):
                data = b''
            else:
                raise SystemExit('unsupported guest file: '+relative)
            emit(relative, info.st_mode, data, info.st_uid, info.st_gid)
    for name in sorted(exclude - {'.dockerenv', '.containerenv'}):
        emit(name, stat.S_IFDIR | (0o1777 if name=='tmp' else 0o755))
    emit('dev/console', stat.S_IFCHR | 0o600, major=5, minor=1)
    emit('dev/null', stat.S_IFCHR | 0o666, major=1, minor=3)
    emit('TRAILER!!!', 0)
print(f'Packed complete isolated guest: payload={payload_bytes} bytes, compressed={(dest / "root.cpio.gz").stat().st_size} bytes, one distribution kernel.')
