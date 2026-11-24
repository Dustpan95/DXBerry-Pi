# WSJT-X wire fixtures

These datagrams use the status-observation layouts in WSJT-X **v3.0.2**, encoded
with real Qt **5.15.15** through PyQt5's QDataStream operators. This is the same
QtCore version installed alongside WSJT-X 3.0.2 on the maintained Pi. The fixture
writer does not import or call the Go parser. No radio, audio device, network
socket or WSJT-X application is opened by the writer.

Sources inspected at the matching upstream tag:

- [Message layouts](https://github.com/WSJTX/wsjtx/blob/v3.0.2/Network/NetworkMessage.hpp),
  blob `74a268fa9bb2b53af7407513aa531cd5c0bf5cf3`.
- [Qt schema selection and header](https://github.com/WSJTX/wsjtx/blob/v3.0.2/Network/NetworkMessage.cpp),
  blob `a1e7050f2322057fadc9345d9443b4871f432c9d`.
- [Actual message writes](https://github.com/WSJTX/wsjtx/blob/v3.0.2/Network/MessageClient.cpp),
  blob `58ebeaa033e80e0276a8f1197dd89537ccfbdb8b`.

The 18 datagrams cover schemas 2/3, current heartbeats, a legacy schema-2
heartbeat, status/decode core and full layouts, null versus empty strings,
nullable QTime, inapplicable numeric sentinels, Clear, Close and an ignored type.
The `.boundaries` files record Qt's stream position after each optional status
field, allowing every partially truncated suffix to be checked independently of
the Go reader. Appended application extensions remain compatible after known
fields. All calls, grids, revision text and messages are fictional test values.

To regenerate from the repository root, with Docker available:

```sh
docker build -f tests/integration/Dockerfile.wsjtx-protocol \
  -t dxberry-wsjtx-qt tests/integration
docker run --rm --network none --user "$(id -u):$(id -g)" \
  -v "$PWD/station/internal/wsjtx/testdata:/out" dxberry-wsjtx-qt /out
```

Qt is required only to regenerate these small checked-in files. Ordinary Go
tests use the files without Qt. The writer records the runtime version in
`qt-version.txt`; review byte changes and that version together. The original
reference run used installed Qt runtime files mounted read-only into an isolated
container because package downloads were unavailable; no dependency was installed
on the Pi or on the host for this check.

These fixtures establish the byte layout and parser behavior. They do not prove
a running WSJT-X instance, endpoint routing, per-radio lifecycle, browser UI,
radio control or RF behavior. Those checks remain separate before activation.
