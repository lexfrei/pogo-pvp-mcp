# syntax=docker/dockerfile:1.7
#
# Multi-stage build for pogo-pvp-mcp. The final image is a scratch
# container with a non-root user, CA certs for the upstream pvpoke
# HTTPS fetch, and the statically-linked binary.
#
# NOTE: the build depends on github.com/lexfrei/pogo-pvp-engine being
# resolvable by `go mod download`, i.e. published + tagged on GitHub.
# During the engine-sibling development window (replace directive in
# go.mod points at ../pogo-pvp-engine) this Containerfile will not
# build cleanly. README documents the prerequisite.

# Image digest deliberately omitted while we're not yet cutting
# releases; renovate/dependabot will pin once the first ghcr.io tag
# ships. 1.26.2 matches the go.mod toolchain floor.
FROM docker.io/library/golang:1.26.2-alpine AS builder

ARG VERSION=development
ARG REVISION=unknown

# hadolint ignore=DL3018
RUN echo 'nobody:x:65534:65534:Nobody:/home/nobody:' > /tmp/passwd && \
    apk add --no-cache ca-certificates && \
    mkdir -p /home/nobody/.cache/pogo-pvp-mcp && \
    chown -R 65534:65534 /home/nobody/.cache

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build \
    -ldflags "-s -w \
        -X github.com/lexfrei/pogo-pvp-mcp/internal/cli.serverVersion=${VERSION} \
        -X github.com/lexfrei/pogo-pvp-mcp/internal/cli.serverRevision=${REVISION}" \
    -trimpath \
    -o /build/pogo-pvp-mcp \
    ./cmd/pogo-pvp-mcp

FROM scratch

COPY --from=builder /tmp/passwd /etc/passwd
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder --chmod=555 /build/pogo-pvp-mcp /pogo-pvp-mcp
COPY --from=builder /build/LICENSE /LICENSE
COPY --from=builder --chown=65534:65534 /home/nobody/.cache /home/nobody/.cache

ENV XDG_CACHE_HOME=/home/nobody/.cache

USER 65534
ENTRYPOINT ["/pogo-pvp-mcp"]
CMD ["serve"]
