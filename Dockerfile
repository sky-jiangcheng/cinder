# Cinder headless server — single-container build (ADR-0025 Docker tier).
# The desktop app stays out of Docker; this image is for NAS/homelab users who
# want `repotory-server` (same-origin web UI) on a volume they can back up.

# --- web dist (server build: base /, output web/dist-server) -----------------
# The Node version comes from .nvmrc (the single source for the whole repo).
# Docker cannot read a file into FROM, so the ARG default is a copy of it —
# scripts/check-version-drift.mjs fails CI when the two drift apart, which is
# the same SSOT-plus-a-gate shape as the app version in wails.json.
#
# Both ARGs below MUST stay ABOVE the first FROM: an ARG declared before any
# FROM is a global ARG and can be referenced by a FROM line, while one declared
# inside a stage is stage-scoped and invisible to the next FROM. Getting this
# wrong fails the build with "UndefinedArgInFrom" and an empty base image
# name (golang:-alpine) — and the local `docker build` is the only place it
# shows up, so it was caught by CI on the first push.
ARG NODE_VERSION=24
ARG GO_VERSION=1.26.10
FROM node:${NODE_VERSION}-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
# build:server ends in ../scripts/prune-web-dist.mjs, which resolves its dist
# path from its own location — so it has to sit at /src/scripts/ exactly, or the
# build dies after the Vite output is already written.
COPY scripts/prune-web-dist.mjs /src/scripts/prune-web-dist.mjs
RUN npm run build:server

# --- server binary (zero CGO, matches the repo's pure-Go SQLite) -------------
# Same shape as NODE_VERSION above: the patch level is pinned, because
# `golang:1.26-alpine` floats to whatever the builder's image cache happens to
# hold — an image can move BACKWARDS, and a shipped binary would then carry a
# Go patch nobody scanned. go.mod's `toolchain` line and every workflow use the
# same version; scripts/check-version-drift.mjs fails CI when they drift apart.
# GO_VERSION itself is declared above the first FROM (it must be global to be
# usable in a FROM line).
FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/repotory-server ./cmd/server

# --- runtime ------------------------------------------------------------------
FROM alpine:3.20
RUN adduser -D -H -u 1000 repotory
COPY --from=build /out/repotory-server /usr/local/bin/repotory-server
COPY --from=web /src/web/dist-server /app/web-dist

# os.UserConfigDir() follows XDG_CONFIG_HOME on Linux, so the whole data
# directory — repotory.db, WAL sidecars and backups/ — lives inside the volume.
ENV XDG_CONFIG_HOME=/data \
    REPOTORY_WEB_DIST=/app/web-dist
VOLUME ["/data"]
EXPOSE 18765
USER repotory
ENTRYPOINT ["repotory-server"]
CMD ["--port", "18765", "--web-dist", "/app/web-dist"]
