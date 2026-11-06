# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath \
    -ldflags "-s -w -X github.com/teslamint/cogvault/internal/version.injected=${VERSION}" \
    -o /out/cogvault ./cmd/cogvault

# ---- Target "kci": the KCI browser worker (ADR 0027, "KCI official-browser
# executor"). Build it explicitly: docker build --target kci .
# These stages sit BEFORE the final stage on purpose: the default target is the
# last stage in the file, so a plain `docker build .` (and the container
# workflow) still builds the unchanged cogvault image, and the browser and its
# Go dependency never enter it.
FROM build AS build-kci
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w" -o /out/cogvault-kci ./cmd/cogvault-kci

# The base image is pinned by digest and Chromium by exact package version, so
# the browser changes only when this file does. Alpine drops a superseded
# chromium from its index, so a stale pin makes the build FAIL (never silently
# upgrade): bump CHROMIUM_VERSION deliberately, re-run the checks in
# docs/deployment/collector.md, and update the digest when the base moves.
FROM alpine:3.22@sha256:bfe7ceb9f09ec0566c48fb93356e45a47a00be5a1dd14fa5b909e7709c66b034 AS kci
ARG CHROMIUM_VERSION=142.0.7444.59-r0
LABEL org.opencontainers.image.title="cogvault-kci" \
      org.opencontainers.image.description="KCI browser worker: headless Chromium plus cogvault-kci. Runs only behind the ADR 0027 activation gate." \
      io.cogvault.chromium-version="${CHROMIUM_VERSION}"
RUN apk add --no-cache ca-certificates tzdata chromium=${CHROMIUM_VERSION} \
    && adduser -D -u 1000 cogvault
USER cogvault
ENV HOME=/tmp
ENTRYPOINT ["/usr/local/bin/cogvault-kci"]
COPY --from=build-kci /out/cogvault-kci /usr/local/bin/cogvault-kci

# git: wiki_delete (and opt-in git.auto_commit) shell out to it.
FROM alpine:3.22
RUN apk add --no-cache ca-certificates git tzdata \
    && adduser -D -u 1000 cogvault
USER cogvault
ENTRYPOINT ["/usr/local/bin/cogvault"]
CMD ["--help"]
COPY --from=build /out/cogvault /usr/local/bin/cogvault
