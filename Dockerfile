# syntax=docker/dockerfile:1

FROM node:20.19.0-alpine3.21 AS web-builder
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.24.0-alpine3.21 AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=web-builder /src/web/dist ./web/dist
ARG VERSION=docker-local
ARG COMMIT=local
ARG BUILD_DATE=unknown
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
    -o /out/sync-hub ./cmd/sync-hub

FROM alpine:3.21
RUN apk add --no-cache ca-certificates su-exec \
    && addgroup -S -g 10001 synchub \
    && adduser -S -D -H -u 10001 -G synchub synchub \
    && mkdir -p /data \
    && chown synchub:synchub /data
COPY --from=go-builder --chown=synchub:synchub /out/sync-hub /usr/local/bin/sync-hub
COPY --chmod=755 docker-entrypoint.sh /usr/local/bin/docker-entrypoint
WORKDIR /data
VOLUME ["/data"]
EXPOSE 8888
USER synchub
ENTRYPOINT ["/usr/local/bin/docker-entrypoint"]
CMD ["-config", "/data/config.yaml", "-listen", "0.0.0.0:8888"]
