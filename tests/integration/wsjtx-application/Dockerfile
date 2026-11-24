FROM golang:1.24.4-bookworm AS compile
WORKDIR /src
COPY station/go.mod station/go.sum ./
RUN GOTOOLCHAIN=local go mod download golang.org/x/sys
COPY station/ ./
RUN --network=none CGO_ENABLED=0 GOTOOLCHAIN=local GOPROXY=off GOMAXPROCS=4 \
    go vet -tags=wsjtxcompat ./internal/wsjtx \
 && CGO_ENABLED=0 GOTOOLCHAIN=local GOPROXY=off GOMAXPROCS=4 \
    go test -c -tags=wsjtxcompat -o /wsjtx-application.test ./internal/wsjtx

FROM debian:trixie-slim AS package
RUN apt-get update && apt-get install -y --no-install-recommends bash curl ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY tests/integration/download-wsjtx.sh /download-wsjtx
RUN bash /download-wsjtx /package

FROM debian:trixie-slim
ENV DEBIAN_FRONTEND=noninteractive
COPY --from=package --chmod=644 /package/wsjtx-3.0.2-linux-x86_64.deb /fixture/wsjtx.deb
RUN apt-get update && apt-get install -y --no-install-recommends \
    /fixture/wsjtx.deb pulseaudio pulseaudio-utils python3-pyqt5.qtmultimedia \
 && rm -rf /var/lib/apt/lists/* /var/cache/apt/*
COPY --from=compile /wsjtx-application.test /fixture/wsjtx-application.test
COPY tests/integration/wsjtx-application/audio-info.py /fixture/audio-info.py
USER 65534:65534
RUN test -r /fixture/wsjtx.deb && test -x /usr/bin/wsjtx
ENV QT_QPA_PLATFORM=offscreen GOMAXPROCS=4
ENTRYPOINT ["/fixture/wsjtx-application.test", "-test.v", "-test.timeout=150s", "-test.run=^TestWSJTXApplicationCompatibility$"]
