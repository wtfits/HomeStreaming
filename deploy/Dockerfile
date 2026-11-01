# syntax=docker/dockerfile:1
# Multi-arch image: docker buildx build --platform linux/amd64,linux/arm64 -f deploy/Dockerfile .

FROM --platform=$BUILDPLATFORM node:22-bookworm AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
COPY scripts/check-es-target.mjs /src/scripts/
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS build
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=docker
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /out/homestreaming ./cmd/homestreaming

FROM ubuntu:24.04
RUN apt-get update \
 && apt-get install -y --no-install-recommends ffmpeg ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/* \
 && (id -u ubuntu >/dev/null 2>&1 && userdel -r ubuntu || true) \
 && useradd --uid 1000 --create-home --shell /usr/sbin/nologin hs \
 && mkdir -p /data && chown hs:hs /data
COPY --from=build /out/homestreaming /usr/local/bin/homestreaming
USER hs
ENV HS_DATA_DIR=/data
VOLUME /data
EXPOSE 8420
HEALTHCHECK --interval=30s --timeout=5s CMD ["/usr/local/bin/homestreaming", "version"]
ENTRYPOINT ["/usr/local/bin/homestreaming"]
CMD ["serve"]
