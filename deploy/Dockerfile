# syntax=docker/dockerfile:1.7

# Arguments with default value (for build).
# Platform options: linux/amd64 or linux/arm64
# ARG PLATFORM=linux/amd64
ARG DISTROLESS_TAG=nonroot
ARG GO_VERSION=1.27

# ------------------------------------------------------------------------------
# Base image Go + Node.js with pnpm package manager.
# ------------------------------------------------------------------------------
FROM golang:${GO_VERSION}-trixie AS base
ENV DO_NOT_TRACK=1 CI=true PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=true
ENV VP_HOME=/opt/vite-plus PNPM_HOME=/pnpm
ENV PATH="/opt/vite-plus/bin:$PNPM_HOME:$PATH"
ENV VP_NODE_MANAGER=yes VP_PM_MANAGER=yes

# Keep in sync with `devEngines.packageManager` in package.json.
ARG NODE_VERSION=24.21
ARG PNPM_VERSION=12.9.1
ARG VP_VERSION=1.0.0

# Install system dependencies (optional tools for building and debugging)
RUN echo 'debconf debconf/frontend select Noninteractive' | debconf-set-selections
RUN --mount=target=/var/lib/apt/lists,type=cache,sharing=locked \
    --mount=target=/var/cache/apt,type=cache,sharing=locked \
    rm -f /etc/apt/apt.conf.d/docker-clean && apt-get update -y \
    && apt-get -yqq --no-install-recommends install build-essential curl \
       inotify-tools pkg-config libssl-dev git unzip ca-certificates jq \
    && update-ca-certificates

# Install the Vite+ global CLI; it manages both the Node.js runtime and pnpm
# (shims live in $VP_HOME/bin). The explicit specs keep the base layer
# deterministic instead of relying on `engines.node` (an open range) to
# auto-resolve. CI=true keeps the installer non-interactive.
SHELL ["/usr/bin/bash", "-c"]
RUN curl -fsSL https://vite.plus | VP_VERSION=${VP_VERSION} bash \
    && vp env install ${NODE_VERSION} pnpm@${PNPM_VERSION} \
    && vp env current

# Add tini for signal handling and zombie reaping
RUN set -eux; \
    TINI_DOWNLOAD_URL="https://github.com/krallin/tini/releases/download/v0.19.0" \
    ARCH="$(dpkg --print-architecture)"; \
    case "${ARCH}" in \
      amd64|x86_64) TINI_BIN_URL="${TINI_DOWNLOAD_URL}/tini" ;; \
      arm64|aarch64) TINI_BIN_URL="${TINI_DOWNLOAD_URL}/tini-arm64" ;; \
      *) echo "unsupported architecture: ${ARCH}"; exit 1 ;; \
    esac; \
    curl -fsSL "${TINI_BIN_URL}" -o /usr/bin/tini; \
    chmod +x /usr/bin/tini

WORKDIR /srv

# ------------------------------------------------------------------------------
# Install dependencies and build the application.
# ------------------------------------------------------------------------------
FROM base AS builder

ENV CGO_ENABLED=0 GOOS=linux
ARG BUILD_VERSION
ARG BUILD_HASH=dev
ARG BUILD_DATE

# Copy dependency manifests first so the dependency layers below stay cached
# across source-only changes (buildkit layer cache). pnpm-workspace.yaml
# carries the install policy (minimumReleaseAge) and must travel with the
# lockfile: without it pnpm falls back to the default release-age policy and
# rejects the frozen lockfile.
COPY --link package.json pnpm-lock.yaml pnpm-workspace.yaml go.mod go.sum ./

# pnpm-lock.yaml records every workspace importer, so each packages/*/package.json
# must exist before `pnpm install --frozen-lockfile` runs.
COPY --link packages/e2e-tests/package.json packages/e2e-tests/
COPY --link packages/email/package.json packages/email/
COPY --link packages/plugins/package.json packages/plugins/
COPY --link packages/webapp/package.json packages/webapp/

# The Vite+ shims resolve node and pnpm without shell setup, so the default
# RUN environment is enough from here on.
SHELL ["/usr/bin/bash", "-c"]

RUN --mount=type=cache,id=pnpm,target=/pnpm/store pnpm install \
    --ignore-scripts --frozen-lockfile --stream

RUN --mount=type=cache,id=go-mod,target=/go/pkg/mod go mod download

# Copy the remaining source files and build the application (typecheck,
# email templates, SPA bundle, and the Go binary are all driven by Vite).
COPY --link . .

# Generate the ConnectRPC code before anything compiles.
# Buf resolves from the module cache prepared above through `go tool`.
RUN --mount=type=cache,id=go-build,target=/root/.cache/go-build \
    go tool buf generate && gofmt -w codegen/proto/go

RUN --mount=type=cache,id=go-build,target=/root/.cache/go-build \
    NODE_ENV=production pnpm exec vp run typecheck \
    && NODE_ENV=production pnpm exec vp run email#build \
    && NODE_ENV=production pnpm exec vp run webapp#build

# ------------------------------------------------------------------------------
# Cleanup the builder stage and create necessary directories.
# ------------------------------------------------------------------------------
FROM base AS pruner

# Copy only necessary files from builder stage
COPY --from=builder /srv/build/release /srv

# Generate default application config and env example
RUN /srv/saka config:generate --overwrite --env-example

# Create necessary data directories and set permissions
RUN mkdir -p /srv/storage/{backup,config,logs,uploads} && chmod -R 0775 /srv/storage

# ------------------------------------------------------------------------------
# Production image, copy build output files and run the application.
# ------------------------------------------------------------------------------
FROM gcr.io/distroless/cc-debian13:${DISTROLESS_TAG} AS runner

# Environment variable with default values
ARG APP_MODE=production
ARG LOG_LEVEL=info
ARG LOG_TRANSPORT=console
ARG SERVER_HOST=0.0.0.0
ARG SERVER_PORT=3000
ARG TZ=UTC

# Copy the build output files and some necessary system utilities from previous stage.
# To enhance security, consider avoiding the copying of sysutils.
COPY --from=base /usr/bin/tini /usr/bin/tini
COPY --from=pruner --chown=nonroot:nonroot /srv /srv

# Define the host and port to listen on.
# Prevent load certs from system (/etc/ssl/certs/ca-certificates.crt)
ENV PATH="/usr/bin:/usr/local/bin:/nodejs/bin:$PATH"
ENV SERVER_HOST=$SERVER_HOST SERVER_PORT=$SERVER_PORT
ENV TZ=$TZ SSL_CERT_FILE= SSL_CERT_DIR=
ENV TINI_SUBREAPER=true

WORKDIR /srv
USER nonroot:nonroot
EXPOSE $SERVER_PORT/tcp

# Define volumes for persistent storage. The config file is generated into
# the image; a deployment that overrides it bind-mounts a file, which a
# VOLUME cannot shadow.
VOLUME /srv/storage

# Healthcheck to monitor application status
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 CMD ["/srv/saka", "hc"]

# SHELL: /busybox/sh
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["/srv/saka", "serve"]
