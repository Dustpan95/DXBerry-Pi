# Qt settings verification

The one-key INI writer must refuse settings that Qt can resolve through another
occurrence or path. The shell regressions run with `tests/run.sh`. The reference
check also reads the files with real Qt; it updates only private temporary files
inside the test container and starts no application, radio or network listener.

```sh
docker build -f tests/integration/Dockerfile.wsjtx-protocol \
  -t dxberry-qt-settings tests/integration
docker run --rm --network none --entrypoint python3 \
  -v "$PWD:/repo:ro" dxberry-qt-settings \
  /repo/tests/integration/qt-ini-check.py /repo
```

Ten checks cover duplicates, percent/Unicode aliases, repeated sections,
root-group paths, whitespace, CRLF, a canonical update, and the real first-run
file. Qt loads sections lazily: the check enumerates all keys before reading
the destination, as WSJT-X profile operations do. BOM/malformed/continuation
refusals are covered separately by shell regressions; they are outside the raw
editor's supported subset.

The original run used Qt 5.15.15 in a network-isolated container with existing
runtime files mounted read-only because environment VPN enrollment failed and
blocked dependency downloads. The normal image recipe above still requires
working package/registry access. No dependencies were installed on the Pi.

QtCore can warn about application-specific QVariant types in the saved first-run
fixture. The test reads the UDP string and verifies every other byte unchanged;
it does not instantiate those program-specific types or prove running-program
compatibility. A passing raw read-back alone is insufficient evidence for an
ambiguous file, and the helper must leave that file unchanged instead.

## Native routing document and file checks

`qt-routing-check.py` reads real Go file transactions through independent Qt.
Build the package's test binary in a Go 1.24.4 container, then run it from the
Qt image above. The test helper stays dormant during ordinary Go test runs;
the Python script invokes it only for private temporary fixtures. The binary
is statically linked and the second container can run as a regular user.

```sh
mkdir -p /tmp/dxberry-qt-routing
docker run --rm --network none \
  -v "$PWD:/repo:ro" -v /tmp/dxberry-qt-routing:/out \
  -w /repo/station -e CGO_ENABLED=0 golang:1.24.4-bookworm \
  go test -c -o /out/wsjtx-settings.test ./internal/wsjtx
docker run --rm --network none --user 1000:1000 --entrypoint python3 \
  -v "$PWD:/repo:ro" -v /tmp/dxberry-qt-routing:/out:ro \
  dxberry-qt-settings /repo/tests/integration/qt-routing-check.py \
  /repo /out/wsjtx-settings.test
```

Seven transaction checks cover canonical, absent, empty and quoted raw values,
saved profiles, literal General groups and the actual first-run fixture. Qt
must read the installed loopback destination, retain saved destinations and
logging/control values, and accept the byte-exact restored original. Fourteen
refusals cover duplicate/escaped/root-path aliases, whitespace, CRLF, unsupported
profile syntax, missing final newline, continuation and multiline quoting.
The root-alias fixture has no competing canonical server key: collision order
must not be assumed from another file's namespace loading order.

The recorded run used the existing builder and read-only mounted Qt 5.15.15
runtime because external package/registry access remained blocked. These checks
do not launch WSJT-X, claim audio devices or send RF. They exercise the file
primitive with a fixture lifecycle check. Durable lease and cross-user bridge
tests are separate; real program-session orchestration remains to connect.
