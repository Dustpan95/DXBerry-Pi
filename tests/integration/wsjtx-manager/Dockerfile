FROM golang:1.24.4-bookworm AS compile
WORKDIR /src
COPY station/go.mod station/go.sum ./
RUN GOTOOLCHAIN=local go mod download golang.org/x/sys
COPY station/ ./
RUN --network=none CGO_ENABLED=0 GOTOOLCHAIN=local GOPROXY=off GOMAXPROCS=4 \
    go vet -tags=wsjtxcompat,wsjtxmanager ./internal/wsjtx \
 && CGO_ENABLED=0 GOTOOLCHAIN=local GOPROXY=off GOMAXPROCS=4 \
    go test -c -tags=wsjtxcompat,wsjtxmanager -o /wsjtx-manager.test ./internal/wsjtx

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
 && rm -rf /var/lib/apt/lists/* /var/cache/apt/* \
 && useradd --system --user-group --create-home --home-dir /var/lib/dxb-wsjtx-home dxbapps \
 && printf 'isolated-wsjtx-manager\n' > /fixture/wsjtx-manager-guest \
 && printf '11223344556677889900aabbccddeeff00\n' > /etc/machine-id
COPY --from=compile /wsjtx-manager.test /fixture/wsjtx-manager.test
COPY tests/integration/wsjtx-application/audio-info.py /fixture/audio-info.py
COPY tests/integration/wsjtx-manager/init /init
COPY tests/integration/wsjtx-manager/manager-test.sh /usr/local/libexec/dxb-wsjtx-manager-test
COPY tests/integration/wsjtx-manager/run-app.sh /fixture/run-app
RUN chmod 755 /init /usr/local/libexec/dxb-wsjtx-manager-test /fixture/run-app

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
