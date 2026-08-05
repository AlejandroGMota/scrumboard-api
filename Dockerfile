# Two stages: build, then a scratch image holding one static binary.
#
# The pure-Go SQLite driver (modernc.org/sqlite) is what makes this possible.
# With cgo there would be a libc to carry, and the image would be an Alpine
# with a shared-library problem waiting to happen.

FROM golang:1.25-alpine AS build

WORKDIR /src

# Dependencies first: this layer is cached until go.mod or go.sum changes,
# which turns most rebuilds into seconds instead of a full module download.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 is what makes the binary static and the final stage scratch.
# -trimpath keeps build machine paths out of the binary; -s -w drop the debug
# symbols, which are of no use in a container anyone would rather restart.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/api ./cmd/api

# An empty /data owned by the user the container runs as.
#
# Docker seeds a *fresh* named volume from whatever is at the mount point in
# the image, ownership included. Without this the volume is created owned by
# root, the unprivileged process cannot open its own database, and the
# container dies with `unable to open database file (14)` — on the first
# deploy, where it looks like a broken image rather than a directory mode.
RUN mkdir -p /out/data

# --- runtime ----------------------------------------------------------------

FROM scratch

# The binary speaks TLS to nothing, but the timezone database matters: every
# timestamp is UTC and time.LoadLocation would fail without it.
COPY --from=build /usr/local/go/lib/time/zoneinfo.zip /zoneinfo.zip
ENV ZONEINFO=/zoneinfo.zip

COPY --from=build /out/api /api

# The SQLite file lives on a volume mounted here. Never `docker compose down -v`
# against this stack: that deletes the board.
COPY --from=build --chown=65534:65534 /out/data /data
VOLUME ["/data"]

# Nothing in this image needs root, and scratch has no user database, so the
# numeric id is the whole story.
USER 65534:65534

EXPOSE 8080

ENV DB_PATH=/data/scrumboard.db \
    PORT=8080

ENTRYPOINT ["/api"]
