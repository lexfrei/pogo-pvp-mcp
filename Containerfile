# syntax=docker/dockerfile:1.7
#
# Multi-stage build for pogo-pvp-mcp. The final image is a scratch
# container with a non-root user, CA certs for the upstream pvpoke
# HTTPS fetch, and the statically-linked binary.
#
# Engine dependency: the go.mod replace directive points at
# ../pogo-pvp-engine on the developer host. At image-build time the
# engine source must be supplied via a named BuildKit context so the
# builder stage can COPY it into a container path; then go mod edit
# rewrites the replace to that in-container path. This removes the
# old "waiting for engine release" blocker — the image builds
# cleanly today against the sibling checkout.
#
# Build invocation (see scripts/build-image.sh):
#
#   docker buildx build \
#     --build-context engine=../pogo-pvp-engine \
#     --tag pogo-pvp-mcp:dev \
#     .

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

# Copy the sibling engine source from the `engine` named build-context
# BEFORE the go.mod / go.sum copy so the replace-directive rewrite
# can point at a path that exists at go-mod-download time.
COPY --from=engine . /build/pogo-pvp-engine

COPY go.mod go.sum ./

# Rewrite the replace directive from the host-relative ../pogo-pvp-engine
# to the in-container absolute path. `go mod edit -replace` is
# idempotent and overrides the existing directive; `go mod download`
# then sees a valid local module.
RUN go mod edit -replace=github.com/lexfrei/pogo-pvp-engine=/build/pogo-pvp-engine && \
    go mod download

COPY . .

# COPY . . clobbered the go.mod we just edited (the host's go.mod
# still has the ../pogo-pvp-engine replace). Re-apply the in-
# container replace before go build — the second edit is cheap and
# removes the need for a .containerignore entry just for go.mod.
RUN go mod edit -replace=github.com/lexfrei/pogo-pvp-engine=/build/pogo-pvp-engine

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

# Document the ports the operator is expected to map. Neither is
# mandatory at runtime — both default to 0 / empty meaning "disabled".
#   8080: public MCP HTTP (Streamable HTTP). Enable via
#         POGO_PVP_SERVER_MCP_HTTP_LISTEN=:8080
#   8787: debug HTTP (/healthz, /refresh, /debug/gamemaster).
#         Loopback-only inside the container; exposing it publicly
#         is a security mistake (see README).
EXPOSE 8080 8787

ENTRYPOINT ["/pogo-pvp-mcp"]
CMD ["serve"]
