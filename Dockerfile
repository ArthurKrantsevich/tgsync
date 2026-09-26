# tgsync node in a container. See docs/en/setup.md#54-docker-compose.
FROM golang:1.27-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /tgsync ./cmd/tgsync

FROM node:22-bookworm-slim
# git for projects and diffs, python3 for plugin hooks. No sudo: the container
# user has no sudo rights, so keep SUDO_MODE=off here.
RUN apt-get update \
 && apt-get install -y --no-install-recommends git python3 ca-certificates curl \
 && rm -rf /var/lib/apt/lists/*
RUN npm install -g @anthropic-ai/claude-code && npm cache clean --force
# Extra system packages your hooks or projects need, e.g. --build-arg EXTRA_PACKAGES="golang make".
ARG EXTRA_PACKAGES=""
RUN if [ -n "$EXTRA_PACKAGES" ]; then apt-get update && apt-get install -y --no-install-recommends $EXTRA_PACKAGES && rm -rf /var/lib/apt/lists/*; fi
COPY --from=build /tgsync /usr/local/bin/tgsync
ENV TGSYNC_HOME=/config
WORKDIR /config
ENTRYPOINT ["tgsync"]
CMD ["run"]
