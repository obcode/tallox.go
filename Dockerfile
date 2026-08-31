# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS builder

ARG VERSION=dev
ARG GIT_COMMIT=none
ARG BUILD_TIME=unknown

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${GIT_COMMIT} -X main.date=${BUILD_TIME}" \
    -o /out/tallox .

# Pinned to the patch version, not `latest` and not `3.24`: only a comparable tag lets
# Dependabot recognise an update. The PR becomes a fix(docker) commit, which produces a
# patch release — and only that rebuilds and rolls out the image. With a floating tag a
# base-image CVE would stay unfixed, because without a release nothing is ever rebuilt.
FROM alpine:3.24.1

# tzdata is mandatory, not a convenience: main.go sets time.Local to Europe/Berlin, and
# milestone deadlines and phase transitions depend on it. Without tzdata the process would
# silently fall back to UTC.
RUN apk add --no-cache ca-certificates tzdata

# Not as root. A fixed UID, so that a mounted volume has predictable ownership.
RUN adduser -D -u 10001 tallox

WORKDIR /app
COPY --from=builder /out/tallox /app/tallox

USER tallox
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/app/tallox"]
