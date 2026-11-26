FROM debian:trixie-slim

ARG DEBIAN_FRONTEND=noninteractive
ARG package_version=3.0.2+dfsg-2~bpo13+1
WORKDIR /packages
RUN test "$(dpkg --print-architecture)" = arm64 \
 && echo 'deb http://deb.debian.org/debian trixie-backports main' > /etc/apt/sources.list.d/backports.list \
 && apt-get update \
 && apt-get download "wsjtx=${package_version}" "wsjtx-data=${package_version}" \
 && echo "651f9ea24d82bf145e07577d66b45cbf57f7605a1f02ed37e144b57c06cef83c  wsjtx_${package_version}_arm64.deb" | sha256sum -c - \
 && echo "e9469f477da8961387ec97d574239dcb5dda1e2a81e758f6501dd1663812e418  wsjtx-data_${package_version}_all.deb" | sha256sum -c - \
 && apt-get install -y --no-install-recommends "./wsjtx_${package_version}_arm64.deb" "./wsjtx-data_${package_version}_all.deb" \
 && rm -rf /packages/*.deb /var/lib/apt/lists/*
COPY --chmod=755 tests/integration/wsjtx-backport/check.sh /check
USER 65534:65534
WORKDIR /tmp
ENTRYPOINT ["/check"]
