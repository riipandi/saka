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
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0 COREPACK_INTEGRITY_KEYS=0 DO_NOT_TRACK=1
ENV LEFTHOOK=0 PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=true CI=true
ENV FNM_PATH=/usr/bin PNPM_HOME=/pnpm HOME=/root
ENV PATH="$HOME/.local/share/fnm:$PNPM_HOME:$PATH"
ARG NODE_VERSION=24.21

# Install system dependencies (optional tools for building and debugging)
RUN echo 'debconf debconf/frontend select Noninteractive' | debconf-set-selections
RUN --mount=target=/var/lib/apt/lists,type=cache,sharing=locked \
    --mount=target=/var/cache/apt,type=cache,sharing=locked \
    rm -f /etc/apt/apt.conf.d/docker-clean && apt-get update -y \
    && apt-get -yqq --no-install-recommends install build-essential curl \
       inotify-tools pkg-config libssl-dev git unzip ca-certificates jq \
    && update-ca-certificates

# Install Node.js and PNPM (we need to use bash as the default shell to load fnm function)
SHELL ["/usr/bin/bash", "-c"]
RUN echo -e "Installing Node.js $NODE_VERSION" && touch $HOME/.bashrc \
    && curl -fsSL https://fnm.vercel.app/install | bash -s -- --skip-shell --install-dir $FNM_PATH \
    && echo 'eval "$(fnm env --use-on-cd --shell bash)"' >> $HOME/.bashrc && . $HOME/.bashrc \
    && fnm install ${NODE_VERSION} --corepack-enabled \
    && fnm default ${NODE_VERSION} --corepack-enabled \
    && corepack enable pnpm

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

# Bash will load fnm function via .bashrc. Since we're starting non-interactive
# shell, we wil need to tell bash to load .bashrc manually.
SHELL ["/usr/bin/bash", "-c"]
ENV BASH_ENV=$HOME/.bashrc

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
    NODE_ENV=production pnpm exec tsc -b --noEmit \
    && NODE_ENV=production pnpm exec vite build

# The embed is compile-time: the SPA bundle and the derived assets.json the
# Vite build wrote above must exist before the Go binary compiles.
RUN --mount=type=cache,id=go-build,target=/root/.cache/go-build \
    go build -tags "release,noasm,nounsafe" -trimpath -buildvcs=false \
    -ldflags "-w -s \
      -X github.com/riipandi/tango/internal/config.AppVersion=${BUILD_VERSION:-dev} \
      -X ${goModule}/internal/config.BuildHash=${BUILD_HASH} \
      -X ${goModule}/internal/config.BuildDate=${BUILD_DATE}" \
    -o build/release/$(jq -r '.name' package.json) ./cmd \
    && strip build/release/$(jq -r '.name' package.json) \
    && chmod +x build/release/$(jq -r '.name' package.json)

# ------------------------------------------------------------------------------
# Cleanup the builder stage and create necessary directories.
# ------------------------------------------------------------------------------
FROM base AS pruner

# Copy only necessary files from builder stage
COPY --from=builder /srv/build/release /srv

# Create necessary data directories and set permissions
RUN mkdir -p /srv/storage/{backup,certs,data,keys,logs,uploads} && \
    chmod -R 0775 /srv/storage && /srv/tango config:generate

# ------------------------------------------------------------------------------
# Production image, copy build output files and run the application.
# ------------------------------------------------------------------------------
FROM gcr.io/distroless/cc-debian13:${DISTROLESS_TAG} AS runner

# Environment variable with default values
ARG APP_MODE=production
ARG APP_LOG_LEVEL=info
ARG APP_LOG_TRANSPORT=console

# Copy the build output files and some necessary system utilities from previous stage.
# To enhance security, consider avoiding the copying of sysutils.
COPY --from=base /usr/bin/tini /usr/bin/tini
COPY --from=pruner --chown=nonroot:nonroot /srv /srv

# Define the host and port to listen on.
# Prevent load certs from system (/etc/ssl/certs/ca-certificates.crt)
ENV PATH="/usr/bin:/usr/local/bin:/nodejs/bin:$PATH"
ARG SERVER_HOST=0.0.0.0 SERVER_PORT=3000 TZ=UTC
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
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 CMD /srv/tango hc

# SHELL: /busybox/sh
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["/srv/tango", "serve"]
