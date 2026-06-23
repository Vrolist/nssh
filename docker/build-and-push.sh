#!/bin/bash
set -e

# =====================================================
# nssh Docker 镜像构建推送脚本
# 用法：
#   ./docker/build-and-push.sh [版本号]
#
# 示例：
#   ./docker/build-and-push.sh              # 生成 dev-年月日时分秒 版本
#   ./docker/build-and-push.sh 1.2.3        # 使用 1.2.3 版本号，并更新 latest
# =====================================================

# 默认镜像名称，可通过环境变量覆盖
IMAGE_NAME=${IMAGE_NAME:-nssh}
IMAGE=${IMAGE_NAME}

# 版本号处理：传参则用参数，否则生成 dev-时间戳
if [ -n "$1" ]; then
    VERSION=$1
else
    VERSION="dev-$(date +%Y%m%d%H%M%S)"
fi

echo "========================================"
echo "Registry: ${REGISTRY}"
echo "Image:    ${IMAGE}"
echo "Version:  ${VERSION}"
echo "========================================"

# 检查 Docker 是否可用
if ! command -v docker >/dev/null 2>&1; then
    echo "Error: docker command not found. Please install Docker first."
    exit 1
fi

# 构建标签参数
TAGS="-t ${IMAGE}:${VERSION}"
# 每次构建都更新 latest 标签
TAGS="${TAGS} -t ${IMAGE}:latest"

# 构建镜像（禁用 BuildKit，使用传统构建器，完整继承 Docker daemon insecure-registries 配置）
echo "Building image..."
DOCKER_BUILDKIT=0 docker build \
    --build-arg VERSION=${VERSION} \
    -f docker/Dockerfile \
    ${TAGS} \
    .

# 推送镜像
echo "Pushing image..."
docker push ${IMAGE}:${VERSION}
docker push ${IMAGE}:latest

echo "========================================"
echo "Done: ${IMAGE}:${VERSION}"
echo "========================================"
