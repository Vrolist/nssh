# =====================================================
# Stage 1: Build - 编译静态二进制
# =====================================================
# BASE_REGISTRY 为空时使用官方镜像，构建时可通过 --build-arg 注入内网地址
# 例如: --build-arg BASE_REGISTRY=192.168.2.88:443/library/
ARG BASE_REGISTRY=
FROM ${BASE_REGISTRY}golang:1.20-alpine AS builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev

ENV GOTOOLCHAIN=local

WORKDIR /src

# 复制依赖文件（利用 Docker 层缓存）
# 使用 goproxy.cn 加速 Go 模块下载
COPY go.mod go.sum ./
COPY base_core/go.mod ./base_core/
COPY base_tunnel/go.mod ./base_tunnel/
COPY platform/go.mod ./platform/
RUN go env -w GOPROXY=https://goproxy.cn,direct && go mod download

COPY . .

# 编译为完全静态链接的二进制
# -s -w : 去掉符号表和调试信息，减小体积
# -trimpath : 去掉编译路径信息
# VERSION 默认 dev，不传参数时自动生成 dev-年月日时分秒
RUN if [ "$VERSION" = "dev" ]; then \
      V="dev-$(date +%Y%m%d%H%M%S)"; \
    else \
      V="$VERSION"; \
    fi && \
    echo "Building nssh version=$V" && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build \
    -ldflags="-s -w -X main.Version=$V" \
    -trimpath \
    -o /nssh \
    main.go

# =====================================================
# Stage 2: Runtime - 最小运行环境
# =====================================================
ARG BASE_REGISTRY=
FROM ${BASE_REGISTRY}alpine:3.18

# ca-certificates: TLS 证书，tzdata: 时区
# 使用清华镜像加速 Alpine 包下载
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.tuna.tsinghua.edu.cn/g' /etc/apk/repositories && \
    apk add --no-cache ca-certificates tzdata && \
    addgroup -S nssh && \
    adduser -S -D -H -s /bin/false -G nssh nssh

COPY --from=builder /nssh /usr/local/bin/nssh
COPY docker/entrypoint.sh /entrypoint.sh

RUN chmod +x /entrypoint.sh && \
    mkdir -p /home/nssh/.nssh && \
    chown -R nssh:nssh /home/nssh

USER nssh
WORKDIR /tmp

ENTRYPOINT ["/entrypoint.sh"]
