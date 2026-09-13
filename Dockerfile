# Two stages so the shipped image is the binary and nothing else. Static
# assets and workflows are embedded, so there is no web root to copy.
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# CGO off keeps it a static binary: modernc.org/sqlite is pure Go, so the
# result runs on Alpine, Debian and a Raspberry Pi alike.
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/gpuless .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -u 10001 gpuless
COPY --from=build /out/gpuless /usr/local/bin/gpuless
# /data must exist and belong to the unprivileged user before VOLUME: a named
# volume inherits the image directory's ownership, and without this the
# container could not write its own database.
RUN mkdir -p /data && chown gpuless:gpuless /data
USER gpuless
WORKDIR /data
VOLUME /data
EXPOSE 8080
ENV GPULESS_DATA=/data GPULESS_ADDR=:8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
  CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["gpuless"]
