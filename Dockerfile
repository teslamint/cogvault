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

# git: wiki_delete (and opt-in git.auto_commit) shell out to it.
FROM alpine:3.22
RUN apk add --no-cache ca-certificates git tzdata \
    && adduser -D -u 1000 cogvault
USER cogvault
ENTRYPOINT ["/usr/local/bin/cogvault"]
CMD ["--help"]
COPY --from=build /out/cogvault /usr/local/bin/cogvault
