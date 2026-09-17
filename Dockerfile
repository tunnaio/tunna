# Build from source; the server is one static binary (ADR-0005).
FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /tunna ./cmd/tunna

# Runtime: no shell, no package manager, non-root (uid 65532). The data
# directory is a volume; a named volume inherits this ownership, a bind
# mount must be writable by 65532.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /tunna /tunna
# No TUNNA_ADDR default here: the binary listens on :8000 unless the
# platform sets PORT or TUNNA_ADDR, and an image default would shadow PORT.
ENV TUNNA_DATA_DIR=/data
VOLUME /data
EXPOSE 8000
ENTRYPOINT ["/tunna"]
