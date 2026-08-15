#!/bin/bash
set -euo pipefail

# =====================================================
# nssh npm 发布脚本
# 用法: ./npm/publish.sh <version> [--dry-run]
#
# 前置条件:
#   1. 各平台二进制已构建在 dist/ 目录，命名约定:
#        nssh_darwin_arm64 / nssh_darwin_amd64
#        nssh_windows_amd64.exe / nssh_linux_amd64 / nssh_linux_arm64
#      （可用 BIN_DIR 环境变量覆盖二进制目录）
#   2. npm 认证已配置（~/.npmrc 的 _authToken，或 NPM_TOKEN 环境变量）
#
# 环境变量:
#   NPM_TOKEN   npm automation token（发布时必须）
#   NPM_REGISTRY 目标 registry（默认 https://registry.npmjs.org）
#   BIN_DIR     二进制所在目录（默认 ../dist）
#
# 发布顺序: 平台包先发 → 主包最后发；已存在版本自动跳过
# =====================================================

VERSION="${1:?用法: publish.sh <version> [--dry-run]}"
DRY_RUN="${2:-}"

cd "$(dirname "$0")"
PKG_ROOT="$(pwd)"

SCOPE="@buladou"
REGISTRY="${NPM_REGISTRY:-https://registry.npmjs.org}"
BIN_DIR="${BIN_DIR:-$(pwd)/../dist}"

# 平台定义: 平台包名:GOOS:GOARCH[:扩展名]
PLATFORMS=(
  "nssh-darwin-arm64:darwin:arm64"
  "nssh-darwin-x64:darwin:amd64"
  "nssh-win32-x64:windows:amd64:exe"
  "nssh-linux-x64:linux:amd64"
  "nssh-linux-arm64:linux:arm64"
)

# 发布单个包（跳过已存在版本）
publish_one() {
  local pkg="$1"
  if [ "$DRY_RUN" = "--dry-run" ]; then
    echo "[dry-run] 将发布 ${pkg}@${VERSION}"
    return 0
  fi
  if npm view "${pkg}@${VERSION}" version --registry "$REGISTRY" >/dev/null 2>&1; then
    echo "[skip] ${pkg}@${VERSION} 已存在，跳过"
    return 0
  fi
  npm publish --access public --registry "$REGISTRY"
  echo "[ok] ${pkg}@${VERSION} 发布成功"
}

# 0. 前置检查
if [ "$DRY_RUN" != "--dry-run" ] && [ -z "${NPM_TOKEN:-}" ] && ! grep -q "_authToken" ~/.npmrc 2>/dev/null; then
  echo "错误: 未找到 npm 认证（NPM_TOKEN 环境变量或 ~/.npmrc 的 _authToken）" >&2
  exit 1
fi
if [ ! -d "$BIN_DIR" ]; then
  echo "错误: 二进制目录不存在: $BIN_DIR" >&2
  exit 1
fi

# 1. 生成并逐个发布平台包
for entry in "${PLATFORMS[@]}"; do
  IFS=':' read -r PKG GOOS GOARCH EXT <<< "$entry"

  SRC="${BIN_DIR}/nssh_${GOOS}_${GOARCH}"
  [ -n "${EXT:-}" ] && SRC="${SRC}.${EXT}"
  if [ ! -f "$SRC" ]; then
    echo "错误: 找不到二进制 $SRC（请先构建）" >&2
    exit 1
  fi

  case "$PKG" in
    *darwin*) OS='"darwin"' ;;
    *win32*)  OS='"win32"' ;;
    *)        OS='"linux"' ;;
  esac
  case "$PKG" in
    *arm64) CPU='"arm64"' ;;
    *)      CPU='"x64"' ;;
  esac

  PKG_DIR="${PKG_ROOT}/packages/${PKG}"
  rm -rf "$PKG_DIR"
  mkdir -p "$PKG_DIR/bin"
  BIN_NAME="nssh"
  [ -n "${EXT:-}" ] && BIN_NAME="nssh.exe"
  cp "$SRC" "${PKG_DIR}/bin/${BIN_NAME}"
  chmod +x "${PKG_DIR}/bin/${BIN_NAME}"

  cat > "${PKG_DIR}/package.json" <<EOF
{
  "name": "${SCOPE}/${PKG}",
  "version": "${VERSION}",
  "description": "nssh native binary for ${GOOS}-${GOARCH}",
  "os": [${OS}],
  "cpu": [${CPU}],
  "files": ["bin"],
  "license": "MIT"
}
EOF

  echo "--- 发布平台包 ${SCOPE}/${PKG}@${VERSION} ---"
  ( cd "$PKG_DIR" && publish_one "${SCOPE}/${PKG}" )
  rm -rf "$PKG_DIR"
done

# 2. 发布主包（版本占位符 0.0.0 → 目标版本，发布后还原）
echo "--- 发布主包 ${SCOPE}/nssh@${VERSION} ---"
sed -i.bak "s/\"0.0.0\"/\"${VERSION}\"/g" "${PKG_ROOT}/package.json"
trap 'sed -i.bak "s/\"${VERSION}\"/\"0.0.0\"/g" "${PKG_ROOT}/package.json" 2>/dev/null; rm -f "${PKG_ROOT}/package.json.bak"' EXIT

if [ "$DRY_RUN" != "--dry-run" ]; then
  if npm view "${SCOPE}/nssh@${VERSION}" version --registry "$REGISTRY" >/dev/null 2>&1; then
    echo "[skip] ${SCOPE}/nssh@${VERSION} 已存在，跳过"
  else
    ( cd "$PKG_ROOT" && npm publish --access public --registry "$REGISTRY" )
    echo "[ok] ${SCOPE}/nssh@${VERSION} 发布成功"
  fi
else
  echo "[dry-run] 将发布 ${SCOPE}/nssh@${VERSION}"
fi

rm -rf "${PKG_ROOT}/packages"

echo ""
echo "完成: ${SCOPE}/nssh@${VERSION}（验证: npx ${SCOPE}/nssh --version）"
