# Multi-stage build. The whole point (DESIGN §16): one static, CGO-free
# binary on a distroless base, so shipping into a closed contour is
# `docker save`/`docker load` of a single small image — no npm registry, no
# vendored toolchain, no compiler in the runtime.

# The admin UI is built here, from source, every image build — never copied
# in from a developer's own `make ui`. .dockerignore excludes
# /internal/webui/dist/ from the context outright, so there is nothing for a
# stray COPY to pick up even by accident: skip or break this stage and the Go
# build below fails loudly on "//go:embed all:dist: pattern all:dist: no
# matching files found" instead of shipping a binary that quietly 503s on
# every UI request.
FROM node:24-alpine AS webbuilder
WORKDIR /src
COPY web/ /src/web/
# api/openapi.json is a BUILD INPUT, not documentation: `yarn gen` (run by the
# build script below) generates the whole API client from it with orval, and
# web/src/api/generated is gitignored precisely so no stale copy can ship.
# Without this COPY the build fails here rather than emitting a client built
# from whatever happened to be lying around.
COPY api/ /src/api/
# The panel's /guide screen compiles docs/USER-GUIDE.md in through a `?raw`
# import (A7, GuidePage.tsx) — one file, listed by name, because
# .dockerignore keeps every other *.md out of the context on purpose.
COPY docs/USER-GUIDE.md /src/docs/USER-GUIDE.md
WORKDIR /src/web
# This box has 7.8 GB of RAM with swap already half used, and its OOM killer
# has already wiped the whole user slice twice in one day (see the Makefile's
# CAP comment for the measured numbers) — capping tsc/vite's own heap turns a
# lost build host into an ordinary failed `yarn build`.
ENV NODE_OPTIONS=--max-old-space-size=1024
# corepack enable makes the `yarn` on PATH the version web/package.json pins
# in packageManager, rather than whatever the base image happens to carry.
# --immutable is npm ci's equivalent: it installs exactly what yarn.lock pins
# and fails on any mismatch instead of silently updating it — why the
# lockfile is committed at all.
RUN corepack enable && yarn install --immutable
# "build" is yarn gen && tsc --noEmit && vite build (web/package.json), so the
# image build regenerates the client from the contract, type-checks it, and
# only then bundles — a contract/handler drift or a type error fails HERE, not
# as a runtime surprise in the shipped SPA. outDir is '../internal/webui/dist'
# relative to this WORKDIR, landing at /src/internal/webui/dist for the
# COPY --from below.
RUN yarn build

FROM golang:1.27.1-alpine AS builder
WORKDIR /src

# go.mod/go.sum first so `go mod download` layer-caches independently of
# source changes.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
# .dockerignore excludes /internal/webui/dist/ outright, so the plain COPY
# above left nothing under it at all — not even .gitkeep. This COPY, landed
# BEFORE go build, is the only thing that puts a real dist/ here at all, so
# //go:embed all:dist (internal/webui/webui.go) embeds an actual SPA.
COPY --from=webbuilder /src/internal/webui/dist /src/internal/webui/dist

# CGO_ENABLED=0: modernc.org/sqlite is pure Go, so the release build never
# needs a C toolchain or glibc — that's what makes distroless/static valid.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mocker ./cmd/mocker

# Staged here only to be copied into the runtime image with the right owner
# (see the COPY below) — distroless has no shell, so mkdir/chown cannot be
# run there.
RUN mkdir /data

FROM gcr.io/distroless/static:nonroot
USER nonroot
COPY --from=builder /out/mocker /mocker

# The data directory must exist in the IMAGE, owned by the runtime user
# (distroless `nonroot` is uid/gid 65532), before it is declared a volume: a
# fresh named volume inherits the image mountpoint's ownership and mode. If
# only VOLUME declares it, the mountpoint is created root:root 0755, the
# unprivileged process cannot create mocker.db in it, and startup dies with
# `open database: ping /data/mocker.db: unable to open database file (14)`.
# 0700 because the volume holds the whole database — nothing else needs in.
COPY --from=builder --chown=65532:65532 --chmod=0700 /data /data
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/mocker"]
