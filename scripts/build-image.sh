#!/usr/bin/env bash
# build-image.sh — build the pogo-pvp-mcp container image locally
# from the sibling pogo-pvp-engine checkout.
#
# The Containerfile's go.mod replace directive points at
# ../pogo-pvp-engine on the host; at image-build time the engine
# source is supplied via a named BuildKit context so the builder
# stage can rewrite the replace to an in-container path. This
# replaces the old "waiting for engine release" blocker — the
# sibling-repo checkout is the source of truth.
#
# Developer-only script: builds a single-arch image for the host
# and loads it into the local daemon via `--load`. CI publishes
# multi-arch images (linux/amd64 + linux/arm64) with push-by-digest
# via `docker/build-push-action` in .github/workflows/release.yml;
# `--load` is incompatible with multi-arch outputs so the two code
# paths deliberately diverge.
#
# Usage:
#   scripts/build-image.sh                      # tag pogo-pvp-mcp:dev
#   scripts/build-image.sh v1.2.3               # tag pogo-pvp-mcp:v1.2.3
#   IMAGE=my/repo/pogo-pvp-mcp scripts/build-image.sh v1.2.3
#
# Environment:
#   IMAGE             image repository (default pogo-pvp-mcp)
#   ENGINE_PATH       path to sibling engine checkout (default
#                     ../pogo-pvp-engine relative to this repo root)
#   VERSION_OVERRIDE  override the ldflags VERSION (default: arg $1 or dev)
#   REVISION_OVERRIDE override the ldflags REVISION (default: git HEAD sha)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

IMAGE="${IMAGE:-pogo-pvp-mcp}"
TAG="${1:-dev}"
ENGINE_PATH="${ENGINE_PATH:-$REPO_ROOT/../pogo-pvp-engine}"
VERSION="${VERSION_OVERRIDE:-$TAG}"
REVISION="${REVISION_OVERRIDE:-$(git rev-parse --short=12 HEAD)}"

if [[ ! -d "$ENGINE_PATH" ]]; then
    echo "ENGINE_PATH does not exist: $ENGINE_PATH" >&2
    echo "Check out github.com/lexfrei/pogo-pvp-engine there, or set ENGINE_PATH=/some/other/path." >&2
    exit 1
fi

# Fail upfront on the common footgun of pointing ENGINE_PATH at the
# wrong checkout — BuildKit would otherwise blow up mid-build with a
# confusing "module github.com/lexfrei/pogo-pvp-engine not found"
# deep in `go mod download`.
ENGINE_GO_MOD="${ENGINE_PATH}/go.mod"
if [[ ! -f "$ENGINE_GO_MOD" ]]; then
    echo "ENGINE_PATH does not contain a go.mod: $ENGINE_PATH" >&2
    echo "Expected the root of a github.com/lexfrei/pogo-pvp-engine checkout." >&2
    exit 1
fi

ENGINE_MODULE_LINE="$(grep --max-count=1 '^module ' "$ENGINE_GO_MOD" || true)"
if [[ "$ENGINE_MODULE_LINE" != "module github.com/lexfrei/pogo-pvp-engine" ]]; then
    echo "ENGINE_PATH go.mod declares the wrong module: $ENGINE_MODULE_LINE" >&2
    echo "Expected: module github.com/lexfrei/pogo-pvp-engine" >&2
    echo "Check out the correct repository or fix ENGINE_PATH." >&2
    exit 1
fi

echo "Building ${IMAGE}:${TAG}" >&2
echo "  ENGINE_PATH=${ENGINE_PATH}" >&2
echo "  VERSION=${VERSION}" >&2
echo "  REVISION=${REVISION}" >&2

docker buildx build \
    --file Containerfile \
    --build-context "engine=${ENGINE_PATH}" \
    --build-arg "VERSION=${VERSION}" \
    --build-arg "REVISION=${REVISION}" \
    --tag "${IMAGE}:${TAG}" \
    --load \
    "$REPO_ROOT"

echo "Built ${IMAGE}:${TAG}" >&2
