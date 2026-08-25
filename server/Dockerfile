# syntax=docker/dockerfile:1.6
# 多阶段构建：编译 → 运行时（alpine，非 root）。
#
# 用法：
#   docker build -t cncachehub:dev -f server/Dockerfile server
#   docker run --rm -p 8080:8080 \
#     -e CNCH_DATA_DIR=/var/lib/cncachehub/data \
#     -v $(pwd)/data:/var/lib/cncachehub/data \
#     cncachehub:dev

# --- build stage ---
FROM golang:1.23-alpine AS build

# 国内 / 海外构建都可能用到，留 ca-certificates 方便 go get 走 https。
RUN apk add --no-cache ca-certificates git

WORKDIR /src

# 优先拷贝 go.mod / go.sum 利用 Docker 缓存。
COPY go.mod go.sum ./
RUN go mod download

# 再拷贝源码。
COPY . .

# CGO_ENABLED=0 保证二进制不依赖 glibc / musl，runtime stage 可以切任何 base。
# -trimpath 移除本地路径，-ldflags="-s -w" 去符号表。
ARG VERSION=dev
ARG COMMIT=local
RUN CGO_ENABLED=0 GOOS=linux GOTOOLCHAIN=local \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/cncachehub ./cmd/cncachehub

# --- runtime stage ---
FROM alpine:3.19

RUN apk add --no-cache ca-certificates wget tzdata \
    && adduser -D -u 10001 app

COPY --from=build /out/cncachehub /usr/local/bin/cncachehub

# 默认数据目录；用 volume 挂载以持久化。
RUN mkdir -p /var/lib/cncachehub/data && chown -R app:app /var/lib/cncachehub
USER app
WORKDIR /var/lib/cncachehub

ENV CNCH_DATA_DIR=/var/lib/cncachehub/data \
    CNCH_HTTP_ADDR=:8080 \
    CNCH_LOG_LEVEL=info \
    TZ=Etc/UTC

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --spider -q http://localhost:8080/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/cncachehub"]
