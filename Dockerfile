# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM node:24-bookworm-slim AS frontend
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml web/.npmrc ./
RUN npm install --global pnpm@11.8.0 && pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM golang:1.25-alpine3.22 AS build

ARG VERSION=dev
ARG REVISION=unknown

RUN apk add --no-cache build-base vips-dev vips-heif

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/web/dist/spa/ ./internal/webui/dist/
RUN CGO_ENABLED=1 go test -tags=libvips ./internal/imageproc
RUN CGO_ENABLED=1 go test -tags=webui ./internal/webui ./internal/app
RUN CGO_ENABLED=1 go build -tags=libvips,webui -trimpath \
    -ldflags="-s -w -X=kaven.xyz/kaven/kaven-media-server/internal/buildinfo.Version=${VERSION} -X=kaven.xyz/kaven/kaven-media-server/internal/buildinfo.Revision=${REVISION}" \
    -o /out/kaven-media ./cmd/kaven-media
RUN CGO_ENABLED=1 go test -c -tags=libvips -o /out/imageproc.test ./internal/imageproc

FROM alpine:3.22
ARG VERSION=dev
ARG REVISION=unknown
LABEL org.opencontainers.image.title="Kaven Media Server" \
      org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$REVISION
RUN apk add --no-cache ca-certificates tzdata vips vips-heif && addgroup -S kaven && adduser -S -G kaven kaven
WORKDIR /app
COPY --from=build /out/kaven-media /usr/local/bin/kaven-media
RUN mkdir -p /data && chown kaven:kaven /data
USER kaven
# Exercise the final libraries as the runtime user without shipping the test binary.
RUN --mount=type=bind,from=build,source=/out/imageproc.test,target=/tmp/imageproc.test \
    /tmp/imageproc.test -test.run='^TestLibvipsCodecMatrix$' -test.v -test.timeout=2m
ENV KAVEN_DATA_DIR=/data KAVEN_LISTEN=:5558
VOLUME ["/data"]
EXPOSE 5558
ENTRYPOINT ["kaven-media"]
CMD ["serve"]
