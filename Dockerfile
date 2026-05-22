# syntax=docker/dockerfile:1.4
# Build one federation-autoscaler component image.
#
# The component is selected at build time via the COMPONENT build-arg and must
# be one of: broker, agent, grpc-server. The Makefile targets docker-build,
# docker-push, docker-buildx all pass --build-arg COMPONENT=<...>.
#
#   docker build --build-arg COMPONENT=broker -t federation-autoscaler/broker:latest .
#
# The agent image additionally bundles `liqoctl` on $PATH so the consumer
# Peer/Unpeer + provider GenerateKubeconfig handlers can shell out to it. The
# broker and grpc-server images stay liqoctl-free — see the runtime-* stages
# below for the split.
#
# Global ARGs go before the first FROM so they can be used inside the final
# FROM runtime-${COMPONENT} directive.
#
# LIQOCTL_BIN points at a host-prefetched binary under bin/liqoctl-<version>-
# <os>-<arch>/liqoctl. The Makefile's docker-build target downloads it before
# invoking docker; we COPY from that path instead of curling github.com from
# inside the build, since some hosts (eg. CrownLabs) blackhole the daemon's
# egress to GitHub Releases.
ARG COMPONENT
ARG LIQOCTL_BIN=bin/liqoctl-v1.1.2-linux-amd64/liqoctl

FROM golang:1.24 AS builder
ARG TARGETOS
ARG TARGETARCH
ARG COMPONENT

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# Cache deps before copying source so source changes don't re-invalidate the
# module download layer.
RUN go mod download

# Copy the Go source (relies on .dockerignore to filter)
COPY . .

# Build the selected component. GOARCH has no default so the image matches the
# host platform by default; --platform / buildx can override.
RUN test -n "${COMPONENT}" || { echo "ERROR: --build-arg COMPONENT=<broker|agent|grpc-server> is required" >&2; exit 1; }
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -a -o app ./cmd/${COMPONENT}

# -----------------------------------------------------------------------------
# Runtime stages, one per component.
#
# broker and grpc-server share the same minimal distroless layer (just the
# Go binary). The agent stage adds liqoctl on /usr/local/bin from the host-
# prefetched binary at $LIQOCTL_BIN. Bump LIQOCTL_VERSION in the Makefile in
# lock-step with the Liqo CRDs the consumer / provider instruction handlers
# create — they must match the cluster's installed Liqo. The final
# `FROM runtime-${COMPONENT}` line picks the matching flavour.
# -----------------------------------------------------------------------------
FROM gcr.io/distroless/static:nonroot AS runtime-broker
COPY --from=builder /workspace/app /app

FROM gcr.io/distroless/static:nonroot AS runtime-grpc-server
COPY --from=builder /workspace/app /app

FROM gcr.io/distroless/static:nonroot AS runtime-agent
ARG LIQOCTL_BIN
COPY --from=builder /workspace/app /app
COPY --from=builder /workspace/${LIQOCTL_BIN} /usr/local/bin/liqoctl

# Pick the right runtime layer based on which component is being built.
FROM runtime-${COMPONENT}
WORKDIR /
USER 65532:65532
ENTRYPOINT ["/app"]
