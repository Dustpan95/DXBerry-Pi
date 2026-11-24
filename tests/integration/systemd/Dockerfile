FROM debian:trixie-slim AS guest
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
    systemd-sysv dbus bash jq util-linux coreutils procps linux-image-amd64 \
 && rm -rf /var/lib/apt/lists/* /var/cache/apt/*
COPY provision/bin/dxberry-radio-event /opt/dxberry/bin/dxberry-radio-event
COPY provision/lib/common.sh /opt/dxberry/lib/common.sh
COPY provision/templates/dxberry-radio-reconcile.service provision/templates/dxberry-radio-reconcile.path provision/templates/dxberry-radio-reconcile.timer /fixture/templates/
COPY tests/integration/systemd/init /init
COPY tests/integration/systemd/manager-test.sh /usr/local/libexec/dxb-manager-test
COPY tests/integration/systemd/inert-apply.sh /usr/local/libexec/dxb-inert-apply
RUN chmod 755 /init /usr/local/libexec/dxb-* /opt/dxberry/bin/dxberry-radio-event \
 && mkdir -p /run/dxb-ci \
 && printf '11223344556677889900aabbccddeeff00\n' > /etc/machine-id

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
