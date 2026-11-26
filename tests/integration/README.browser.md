# Browser audio/PTT integration

Run the real station pages, HTTP handlers, ownership lock, PTT controller and Pion
WebRTC bridge against a simulated radio and generated audio. Chromium uses its
test microphone. No radio, sound device, service manager or live station is used.
The fixture is compiled only with the `browserfixture` build tag and explicitly
enabled at runtime; it is absent from the production binary. Authentication uses
a fixed fixture session, so this is not a login or certificate-trust test.

```sh
docker build -t dxberry-browser -f tests/integration/Dockerfile.browser .
docker run --rm --network none --shm-size=256m dxberry-browser
```

To retain generated status, screenshots and server logs, mount a writable directory
at `/evidence` and set `DXB_BROWSER_OUTPUT=/evidence`. The browser communicates only
with the fixture on loopback, including real Opus/WebRTC traffic. Transmit commands
are recorded by an in-memory writer; playback bytes go to a temporary file.

The checks cover STOP-label contrast in both light and dark themes, receive
audio, test-microphone audio while keyed, pointer and
keyboard release, STOP, a continuing hold's 30-second timeout, immediate unkey on
audio-bridge close, narrow dark-theme touch input, and closing a transmitting tab.
Telemetry polling is sequential and uncached; it distinguishes an opening PTT
window from the radio writer's keyed state and waits for teardown to finish.

This supplements the Go and fake-DOM tests. It does not establish RF keying,
physical audio quality, real sound-server integration, radio latency/SWR, Safari
or device permission behavior, or two-radio hardware operation.

## Read-only native WSJT-X status

Run the second fixture workflow with the same image:

```sh
docker run --rm --network none --shm-size=256m dxberry-browser bash tests/integration/browser-wsjtx.sh
```

`DXB_BROWSER_PROGRAM=wsjtx` activates a fixture-only observation provider. The
real production HTTP handlers, authenticated events socket, bounded event
producer and embedded radio page render synthetic status and decode records.
The test does not run WSJT-X, systemd or a sound device. Its control endpoints
exist only in the tagged fixture and are absent from the production binary.

Coverage includes last-reported values and their independent age, protocol idle
expiry without new frames, loading/error/off/recovery/maintenance states,
literal long text, complete bounded history, keyboard expansion, phone layout,
light/dark/reduced-motion/forced-colors behavior and reconnect recovery. The
dashboard fragment reveals and focuses the panel once. Recorded requests and
socket messages verify that reading and refreshing status do not change radio
or collection state; the simulated transmitter remains unkeyed. Output follows
`DXB_BROWSER_OUTPUT` as above. These checks establish neither compatibility with
the actual WSJT-X application nor physical radio/device acceptance.

The HTTPS login case starts the production console and nginx station site
inside the isolated image. An ephemeral CA signs the test host certificate,
which Chromium validates with its private trust store. A disposable OS
administrator logs in using the normal form and PAM. The check follows the
session through reload and logout, verifies the cookie attributes, and checks
anonymous and authenticated responses. It uses no station accounts or radio devices.
