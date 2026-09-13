# GitHub Stories service image: one image, two entry points.
#
#   docker run ghcr.io/alliecatowo/gh-stories api
#   docker run ghcr.io/alliecatowo/gh-stories worker
#
# Deliberately NOT distroless or scratch: the worker shells out to ffmpeg to
# transcode hostile user media, and pretending video works without ffmpeg
# present would be a lie. The runtime is therefore a slim Debian with ffmpeg
# and nothing else, running as a non-root user with no shell.

# ---------------------------------------------------------------- build stage
FROM --platform=$BUILDPLATFORM golang:1.27.1-bookworm AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ARG DEFAULT_SERVICE_URL=

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# No ghs_testidp tag: the demo-only sign-in route is never compiled into a
# published image.
RUN set -eux; \
    PKG=github.com/alliecatowo/gh-stories/internal/version; \
    LDFLAGS="-s -w -X ${PKG}.Version=${VERSION} -X ${PKG}.Commit=${COMMIT} -X ${PKG}.BuildDate=${BUILD_DATE}"; \
    if [ -n "${DEFAULT_SERVICE_URL}" ]; then \
      LDFLAGS="${LDFLAGS} -X ${PKG}.DefaultServiceURL=${DEFAULT_SERVICE_URL}"; \
    fi; \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
      go build -trimpath -ldflags "${LDFLAGS}" -o /out/gh-stories-api ./apps/api; \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
      go build -trimpath -ldflags "${LDFLAGS}" -o /out/gh-stories-worker ./apps/worker

# -------------------------------------------------------------- runtime stage
FROM debian:bookworm-slim AS runtime

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ffmpeg ca-certificates curl; \
    rm -rf /var/lib/apt/lists/*; \
    # A service account with no shell and no home: the media worker parses
    # attacker-supplied files, so it must not be able to log in anywhere.
    groupadd --system --gid 10001 stories; \
    useradd --system --uid 10001 --gid stories --no-create-home \
            --shell /usr/sbin/nologin stories

COPY --from=build /out/gh-stories-api    /usr/local/bin/gh-stories-api
COPY --from=build /out/gh-stories-worker /usr/local/bin/gh-stories-worker
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod 0755 /usr/local/bin/docker-entrypoint.sh

USER stories:stories
WORKDIR /tmp
EXPOSE 8787

ENV GHS_HTTP_ADDR=:8787

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD curl -fsS http://127.0.0.1:8787/v1/health/ready >/dev/null || exit 1

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
LABEL org.opencontainers.image.title="GitHub Stories" \
      org.opencontainers.image.description="Stories for GitHub. The API and media worker." \
      org.opencontainers.image.source="https://github.com/alliecatowo/gh-stories" \
      org.opencontainers.image.url="https://alliecatowo.github.io/gh-stories/" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}"

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["api"]
