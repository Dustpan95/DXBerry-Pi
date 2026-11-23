FROM debian:trixie-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends bash curl jq ca-certificates util-linux \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /repo
