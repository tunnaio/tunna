# Build from source; the server is one static binary (ADR-0005). The build
# stage runs on the builder's own platform and cross-compiles for the
# target, so a multi-platform build never runs the Go compiler under
# emulation.
FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /tunna ./cmd/tunna
# The data directory must exist in the image owned by the runtime user:
# VOLUME creates it as root otherwise, and the non-root server cannot open
# its database there.
RUN mkdir -p /data && chown 65532:65532 /data

# Runtime: no shell, no package manager, non-root (uid 65532). The data
# directory is a volume; a named volume inherits this ownership, a bind
# mount must be writable by 65532.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /tunna /tunna
COPY --from=build --chown=65532:65532 /data /data
# No TUNNA_ADDR default here: the binary listens on :8000 unless the
# platform sets PORT or TUNNA_ADDR, and an image default would shadow PORT.
ENV TUNNA_DATA_DIR=/data
VOLUME /data
EXPOSE 8000
ENTRYPOINT ["/tunna"]
