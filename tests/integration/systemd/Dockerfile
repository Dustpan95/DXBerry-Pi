FROM golang:1.24.4-bookworm AS bundles
RUN apt-get update && apt-get install -y --no-install-recommends libpam0g-dev libopus-dev pkg-config \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY station/go.mod station/go.sum ./station/
RUN cd station && GOTOOLCHAIN=local go mod download
COPY station/ ./station/
COPY provision/ ./provision/
COPY build/make-update-tarball.sh ./build/
RUN --network=none set -eu; export GOTOOLCHAIN=local GOPROXY=off; \
    for version in 0.0.1 0.0.2; do \
      bash station/build.sh ../provision/bin/dxberryd "$version"; \
      bash build/make-update-tarball.sh "$version" /packages; \
    done; \
    mkdir /baseline; \
    tar -xzf /packages/dxberry-pi-0.0.1.tar.gz --strip-components=1 -C /baseline

FROM debian:trixie-slim AS guest
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
    systemd-sysv dbus bash jq util-linux coreutils procps curl iproute2 libopus0 linux-image-amd64 \
 && rm -rf /var/lib/apt/lists/* /var/cache/apt/*
COPY --from=bundles /baseline/ /opt/dxberry/
COPY --from=bundles /packages/dxberry-pi-0.0.2.tar.gz* /fixture/packages/
COPY tests/integration/systemd/update-roundtrip.sh /usr/local/libexec/dxb-update-roundtrip
COPY --chmod=755 tests/integration/systemd/check-update-setup.sh /fixture/check-update-setup
COPY provision/templates/dxberry-radio-reconcile.service provision/templates/dxberry-radio-reconcile.path provision/templates/dxberry-radio-reconcile.timer /fixture/templates/
COPY tests/integration/systemd/init /init
COPY tests/integration/systemd/manager-test.sh /usr/local/libexec/dxb-manager-test
COPY tests/integration/systemd/inert-apply.sh /usr/local/libexec/dxb-inert-apply
RUN chmod 755 /init /usr/local/libexec/dxb-* /opt/dxberry/bin/dxberry-radio-event \
 && mkdir -p /run/dxb-ci \
 && printf '11223344556677889900aabbccddeeff\n' > /etc/machine-id

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
