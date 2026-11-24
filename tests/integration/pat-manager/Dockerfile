FROM golang:1.24.4-bookworm AS compile
WORKDIR /src
COPY station/go.mod station/go.sum ./
COPY station/internal/pat ./internal/pat
RUN --network=none CGO_ENABLED=0 GOTOOLCHAIN=local GOPROXY=off GOMAXPROCS=4 \
    go test -c -tags=patcompat,patmanager -o /pat-manager.test ./internal/pat

FROM debian:trixie-slim AS package
RUN apt-get update && apt-get install -y --no-install-recommends bash curl ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY tests/integration/download-pat.sh /download-pat
RUN bash /download-pat /package

FROM debian:trixie-slim AS guest
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
    systemd-sysv dbus bash util-linux coreutils procps iproute2 libax25 linux-image-amd64 \
 && rm -rf /var/lib/apt/lists/* /var/cache/apt/*
COPY --from=package /package/pat_1.0.1_linux_amd64.deb /fixture/pat_1.0.1_linux_amd64.deb
RUN dpkg --install /fixture/pat_1.0.1_linux_amd64.deb \
 && useradd --system --user-group --create-home --home-dir /var/lib/dxb-pat-home dxbapps \
 && printf 'isolated-pat-manager\n' > /fixture/pat-manager-guest \
 && printf '11223344556677889900aabbccddeeff00\n' > /etc/machine-id
COPY --from=compile /pat-manager.test /fixture/pat-manager.test
COPY tests/integration/pat-manager/init /init
COPY tests/integration/pat-manager/manager-test.sh /usr/local/libexec/dxb-pat-manager-test
RUN chmod 755 /init /usr/local/libexec/dxb-pat-manager-test

FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    qemu-system-x86 python3 coreutils ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=guest / /guest/
COPY tests/integration/systemd/pack.py /pack.py
RUN mkdir /vm && python3 /pack.py /guest /vm && rm -rf /guest /pack.py
COPY tests/integration/systemd/run.sh /run-test
RUN chmod 755 /run-test
ENTRYPOINT ["/run-test"]
