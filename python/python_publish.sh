#!/bin/bash
set -euo pipefail

# =====================================================
# nssh Python PyPI 发布脚本
# 用法: ./python/python_publish.sh <version> [--dry-run]
#
# 方案 A：多平台 wheel，pip 依平台标签自动选择
#
# 前置条件:
#   1. 各平台二进制已在 ../dist（或 BIN_DIR 覆盖），命名:
#        nssh_darwin_arm64 / nssh_darwin_amd64
#        nssh_linux_amd64 / nssh_linux_arm64
#        nssh_windows_amd64.exe
#   2. 发布认证：~/.pypirc（__token__）或 TWINE_PASSWORD 环境变量
#   3. 构建工具：pip install build twine
#
# 环境变量:
#   BIN_DIR      二进制所在目录（默认 ../dist）
#   TWINE_REPOSITORY_URL 目标仓库（默认 https://upload.pypi.org/legacy/）
# =====================================================

VERSION="${1:?用法: python_publish.sh <version> [--dry-run]}"
DRY_RUN="${2:-}"

cd "$(dirname "$0")"
PY_ROOT="$(pwd)"
REPO_ROOT="$(cd .. && pwd)"

BIN_DIR="${BIN_DIR:-$REPO_ROOT/dist}"
WHEEL_DIR="${PY_ROOT}/wheelhouse"
PKG_SRC="${PY_ROOT}/nssh"
PYPROJECT="${PY_ROOT}/pyproject.toml"

# 平台定义: GO 源文件名 : wheel 平台标签
# darwin arm64 需 macosx_11_0_arm64（11.0 起才支持 arm64）
# darwin x64 用 macosx_10_9_x86_64 覆盖更广
# linux 用 manylinux_2_17_*（对应 glibc 2.17）
PLATFORMS=(
  "nssh_darwin_arm64:macosx_11_0_arm64"
  "nssh_darwin_amd64:macosx_10_9_x86_64"
  "nssh_linux_amd64:manylinux_2_17_x86_64"
  "nssh_linux_arm64:manylinux_2_17_aarch64"
  "nssh_windows_amd64.exe:win_amd64"
)

if [ "$DRY_RUN" == "--dry-run" ] || [ -z "${TWINE_REPOSITORY_URL:-}" ]; then
  : # dry-run 或未指定则不校验
fi

if [ ! -d "$BIN_DIR" ]; then
  echo "错误: 二进制目录不存在: $BIN_DIR" >&2
  exit 1
fi

# 构建工具检查
python3 -m build --version >/dev/null 2>&1 || { echo "缺少 build: pip install build" >&2; exit 1; }
twine --version >/dev/null 2>&1 || { echo "缺少 twine: pip install twine" >&2; exit 1; }

# 输出目录（含版本号，便于跳过已存在）
DIST_DIR="${PY_ROOT}/dist_${VERSION}"
rm -rf "$DIST_DIR" "$WHEEL_DIR"
mkdir -p "$DIST_DIR" "$WHEEL_DIR"

# 0. 将 pyproject.toml 的版本占位符替换为目标版本，构建后还原
#    sed -i.bak 会把原始文件保存为 pyproject.toml.bak，退出时用备份还原
set_placeholder_values() {
  sed -i.bak "s/^version = \"0.0.0\"/version = \"${VERSION}\"/" "$PYPROJECT"
}
restore_placeholder_values() {
  if [ -f "${PYPROJECT}.bak" ]; then
    mv "${PYPROJECT}.bak" "$PYPROJECT"
  fi
}
set_placeholder_values
trap 'restore_placeholder_values' EXIT

# 清理上一次构建残留，避免多平台 wheel 相互污染
cleanup_build_artifacts() {
  rm -rf "$PY_ROOT/build" "$PY_ROOT"/*.egg-info
}

# 1. 逐个平台构建 wheel
BUILT=0
for entry in "${PLATFORMS[@]}"; do
  IFS=':' read -r SRC PLAT <<< "$entry"

  if [ ! -f "${BIN_DIR}/${SRC}" ]; then
    echo "[skip] 缺少二进制 ${BIN_DIR}/${SRC}，跳过平台 ${PLAT}"
    continue
  fi

  # 放入包目录并保留执行位
  mkdir -p "${PKG_SRC}/bin"
  cp "${BIN_DIR}/${SRC}" "${PKG_SRC}/bin/nssh"
  chmod +x "${PKG_SRC}/bin/nssh"

  echo "--- 构建 wheel: ${PLAT} ---"
  cleanup_build_artifacts
  python3 -m build --wheel --outdir "$WHEEL_DIR" \
    --config-setting="--plat-name=${PLAT}" "$PY_ROOT"

  rm -f "${PKG_SRC}/bin/nssh"
  BUILT=$((BUILT + 1))
done

rm -rf "${PKG_SRC}/bin"
cleanup_build_artifacts

if [ "$BUILT" -eq 0 ]; then
  echo "错误: 没有找到任何平台的二进制，未构建任何 wheel" >&2
  exit 1
fi

# 2. 汇总 wheel 到输出目录
cp "$WHEEL_DIR"/*.whl "$DIST_DIR/"

echo ""
echo "产出的 wheel:"
for w in "$DIST_DIR"/*.whl; do echo "  $w"; done

# 3. 上传
if [ "$DRY_RUN" == "--dry-run" ]; then
  echo ""
  echo "[dry-run] 未执行上传，可执行： twine upload ${DIST_DIR}/*.whl"
  exit 0
fi

if [ -z "${TWINE_USERNAME:-}" ]; then
  TWINE_USERNAME="__token__"
fi

# 未显式提供 TWINE_PASSWORD 且存在 ~/.pypirc 时，交给 twine 自身读取配置
if [ -z "${TWINE_PASSWORD:-}" ]; then
  echo "提示: 使用 ~/.pypirc 中的认证（或设置 TWINE_PASSWORD 环境变量）"
fi

REPO_URL="${TWINE_REPOSITORY_URL:-https://upload.pypi.org/legacy/}"

echo ""
echo "--- 上传到 PyPI (${REPO_URL}) ---"
if [ -n "${TWINE_PASSWORD:-}" ]; then
  TWINE_USERNAME="$TWINE_USERNAME" TWINE_PASSWORD="$TWINE_PASSWORD" \
    twine upload --repository-url "$REPO_URL" "$DIST_DIR"/*.whl
elif [ -f "$HOME/.pypirc" ]; then
  TWINE_USERNAME="$TWINE_USERNAME" twine upload --repository-url "$REPO_URL" "$DIST_DIR"/*.whl
else
  echo "错误: 未提供上传凭证。请设置 TWINE_PASSWORD 或配置 ~/.pypirc" >&2
  exit 1
fi

echo ""
echo "完成: nssh@${VERSION}（验证: pip install nssh==${VERSION} && nssh --version）"