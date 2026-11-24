FROM golang:1.24.4-bookworm AS compile
RUN apt-get update && apt-get install -y --no-install-recommends libpam0g-dev libopus-dev pkg-config \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY station/go.mod station/go.sum ./
RUN GOTOOLCHAIN=local go mod download
COPY station/ ./
RUN --network=none CGO_ENABLED=0 GOTOOLCHAIN=local GOPROXY=off GOMAXPROCS=4 \
    go vet -tags=wsjtxcompat,wsjtxmanager,wsjtxscreen ./internal/wsjtx \
 && CGO_ENABLED=0 GOTOOLCHAIN=local GOPROXY=off GOMAXPROCS=4 \
    go test -c -tags=wsjtxcompat,wsjtxmanager,wsjtxscreen -o /wsjtx-manager.test ./internal/wsjtx \
 && CGO_ENABLED=0 GOTOOLCHAIN=local GOPROXY=off GOMAXPROCS=4 \
    go build -trimpath -o /dxberry-wsjtx-settings ./cmd/dxberry-wsjtx-settings \
 && CGO_ENABLED=1 GOTOOLCHAIN=local GOPROXY=off GOMAXPROCS=4 \
    go build -tags=nolibopusfile -trimpath -o /dxberryd ./cmd/dxberryd

FROM debian:trixie-slim AS package
RUN apt-get update && apt-get install -y --no-install-recommends bash curl ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY tests/integration/download-wsjtx.sh /download-wsjtx
RUN bash /download-wsjtx /package

FROM debian:trixie-slim AS guest
ENV DEBIAN_FRONTEND=noninteractive
COPY --from=package --chmod=644 /package/wsjtx-3.0.2-linux-x86_64.deb /fixture/wsjtx.deb
RUN apt-get update && apt-get install -y --no-install-recommends \
    /fixture/wsjtx.deb pulseaudio pulseaudio-utils python3-pyqt5.qtmultimedia \
    systemd-sysv dbus bash util-linux coreutils procps iproute2 linux-image-amd64 \
    tigervnc-standalone-server openbox xauth libpam0g libpam-modules libpam-runtime passwd libopus0 \
 && rm -rf /var/lib/apt/lists/* /var/cache/apt/* \
 && useradd --system --user-group --create-home --home-dir /var/lib/dxberry-apps dxbapps \
 && groupadd -f sudo \
 && useradd --create-home --user-group --groups sudo fixtureadmin \
 && printf 'isolated-wsjtx-manager\n' > /fixture/wsjtx-manager-guest \
 && printf '11223344556677889900aabbccddeeff00\n' > /etc/machine-id
COPY --from=compile /wsjtx-manager.test /fixture/wsjtx-manager.test
COPY --from=compile /dxberry-wsjtx-settings /opt/dxberry/bin/dxberry-wsjtx-settings
COPY --from=compile /dxberryd /opt/dxberry/bin/dxberryd
COPY tests/integration/wsjtx-application/audio-info.py /fixture/audio-info.py
COPY tests/integration/wsjtx-manager/init /init
COPY provision/templates/pam-dxberry /etc/pam.d/dxberry
COPY provision/templates/dxberryd.service /fixture/dxberryd.service
COPY tests/integration/wsjtx-daemon/daemon-test.sh /usr/local/libexec/dxb-wsjtx-manager-test
COPY tests/integration/wsjtx-screen/run-session.sh /fixture/run-session
COPY provision/lib/ /fixture/lib/
RUN chmod 755 /init /usr/local/libexec/dxb-wsjtx-manager-test /fixture/run-session

FROM debian:trixie-slim
ENV DXB_GUEST_MEMORY_MIB=4096
RUN apt-get update && apt-get install -y --no-install-recommends \
    qemu-system-x86 python3 coreutils ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=guest / /guest/
COPY tests/integration/systemd/pack.py /pack.py
RUN mkdir /vm && python3 /pack.py /guest /vm && rm -rf /guest /pack.py
COPY tests/integration/systemd/run.sh /run-test
RUN chmod 755 /run-test
ENTRYPOINT ["/run-test"]
