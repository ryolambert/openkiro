# syntax=docker/dockerfile:1
# Dockerfile — main openkiro image
#
# Builds a minimal, statically linked openkiro binary and packages it in a
# distroless/static base image (no shell, no package manager, minimal attack
# surface). The resulting image is suitable for running openkiro server inside
# a Docker environment or as a sidecar container.
#
# Build:
#   docker build -t openkiro:latest .
#
# Run:
#   docker run --rm -p 127.0.0.1:1234:1234 \
#     -v ~/.aws:/home/nonroot/.aws:ro \
#     openkiro:latest server

# ── Stage 1: build ──────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

WORKDIR /build

# Cache dependency downloads separately from source compilation.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w \
        -X main.version=${VERSION} \
        -X main.commit=${COMMIT} \
        -X main.date=${BUILD_DATE}" \
      -o /openkiro \
      ./cmd/openkiro

# ── Stage 2: runtime ────────────────────────────────────────────────────────
# gcr.io/distroless/static-debian12 contains no shell or libc — only CA certs
# and the binary we copy in. The "nonroot" tag runs as UID 65532 by default.
FROM gcr.io/distroless/static-debian12:nonroot

# Copy the statically linked binary.
COPY --from=builder /openkiro /openkiro

# Proxy port (can be overridden with $OPENKIRO_PORT).
EXPOSE 1234

ENTRYPOINT ["/openkiro"]
CMD ["server"]
