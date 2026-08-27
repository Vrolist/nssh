#!/usr/bin/env bash
# Debian 打包发版脚本（第 1 步，VM 端） —— 在 Debian 13 VM 的 /root/nssh 里执行。
# 用法: ./scripts/debian-1-vm.sh [version] [--full]
#   version    与上游 tag 对齐的版本号。省略时从最新 git tag 推荐候选
#              （与 release.sh 相同的 patch/minor/major 递增逻辑）
#   --full     跑全量验证（sbuild/lintian/pbuilder/autopkgtest），
#              默认只跑 sbuild+lintian（足够覆盖纯版本号变更）
set -euo pipefail

# dch 维护者身份（与 GPG/Salsa 一致）+ 消除 locale/DEBEMAIL 警告
export LC_ALL=C.UTF-8
export DEBFULLNAME="${DEBFULLNAME:-buladou (SongHu)}"
export DEBEMAIL="${DEBEMAIL:-kellyhu258@gmail.com}"

VERSION=""
FULL=""
for arg in "$@"; do
    case "$arg" in
        --full) FULL="--full" ;;
        *) VERSION="$arg" ;;
    esac
done

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
CHROOT="trixie-amd64-sbuild"
PBUILDER_BASE="/var/cache/pbuilder/base-trixie.tgz"
MIRROR="https://mirrors.tuna.tsinghua.edu.cn/debian/"

cd "$REPO_DIR"

echo "==> [1/5] 拉取 gitea 最新代码"
git pull --ff-only

if [ -z "$VERSION" ]; then
    # 从最新 tag 推荐候选版本（tag 是"上一个"，所以候选是其递增版）
    CURRENT_TAG=$(git describe --tags --abbrev=0 2>/dev/null || echo "")
    if [ -z "$CURRENT_TAG" ]; then
        echo "错误: 无 git tag 且未传版本号。用法: $0 <version>"
        exit 1
    fi
    IFS='.' read -r MAJOR MINOR PATCH <<< "${CURRENT_TAG#v}"
    PATCH_VER="${MAJOR}.${MINOR}.$((PATCH + 1))"
    MINOR_VER="${MAJOR}.$((MINOR + 1)).0"
    MAJOR_VER="$((MAJOR + 1)).0.0"
    # changelog 若已 bump 且领先于 tag，作为推荐项
    DEB_VERSION=$(sed -n 's/^nssh (\([^)-]*\)).*/\1/p' debian/changelog | head -1)
    CHOICE_DEB=""
    if [ -n "$DEB_VERSION" ] && [ "v${DEB_VERSION}" != "$CURRENT_TAG" ]; then
        CHOICE_DEB=0
        echo ""
        echo "  0) ${DEB_VERSION}  (from debian/changelog — recommended)"
    fi
    echo "当前 tag: ${CURRENT_TAG}"
    echo ""
    echo "  1) ${PATCH_VER}  (patch - bug fixes)"
    echo "  2) ${MINOR_VER}  (minor - new features)"
    echo "  3) ${MAJOR_VER}  (major - breaking changes)"
    echo "  4) 自定义版本号"
    echo "  q) 取消"
    echo ""
    read -p "选择 Debian 版本: " CHOICE
    case "${CHOICE:-x}" in
        0) VERSION="$DEB_VERSION" ;;
        1) VERSION="$PATCH_VER" ;;
        2) VERSION="$MINOR_VER" ;;
        3) VERSION="$MAJOR_VER" ;;
        4)
            read -p "输入版本号 (如 0.28.0): " VERSION
            [ -n "$VERSION" ] || { echo "版本不能为空"; exit 1; } ;;
        q|Q) echo "已取消"; exit 0 ;;
        *) echo "无效选择"; exit 1 ;;
    esac
fi

echo "==> [2/5] 更新 debian/changelog 到 ${VERSION}"
# 上次运行中断（如 Ctrl+C）会残留 dch 备份文件，导致下次 dch 拒绝执行
if [ -f debian/changelog.dch ]; then
    echo "    清理上次中断残留的 debian/changelog.dch"
    rm -f debian/changelog.dch
fi
# -D trixie: 避免 dch 默认的 UNRELEASED 触发 lintian E: unreleased-changes
dch -v "${VERSION}" -D trixie "new upstream release"

echo "==> [3/5] 生成源包"
dpkg-buildpackage -S -us -uc -d
DSC="$(dirname "$REPO_DIR")/nssh_${VERSION}.dsc"
[ -f "$DSC" ] || { echo "错误: 未生成 $DSC"; exit 1; }

echo "==> [4/5] sbuild 干净构建"
OUT_DIR="$(dirname "$REPO_DIR")"
# 显式指定输出目录，避免不同 sbuild 版本产物落盘位置不一致；
# 若有残留 session 导致 lintian 异常，可先用 schroot --end-session 清理
sbuild --arch=amd64 --dist=trixie --chroot="$CHROOT" \
    --output-dir="$OUT_DIR" "$DSC"

BUILD_LOG="$(ls -t "$OUT_DIR"/nssh_${VERSION}_amd64*.build 2>/dev/null | head -1)"
if [ -z "$BUILD_LOG" ]; then
    echo "警告: 未找到 ${OUT_DIR}/nssh_${VERSION}_amd64*.build，跳过 lintian 结果统计"
    echo "排查: find / -name 'nssh_${VERSION}_amd64*'"
else
    WARNINGS=$(grep -c '^W:' "$BUILD_LOG" || true)
    ERRORS=$(grep -c '^E:' "$BUILD_LOG" || true)
    echo "    lintian: ${ERRORS} errors, ${WARNINGS} warnings"
    if [ "$ERRORS" != "0" ]; then
        grep '^E:' "$BUILD_LOG"
        echo "存在 lintian error，中止"; exit 1
    fi
fi

if [ "$FULL" = "--full" ]; then
    echo "==> pbuilder 可重复构建"
    pbuilder --build --distribution trixie --mirror "$MIRROR" \
        --basetgz "$PBUILDER_BASE" "$DSC"
    echo "==> autopkgtest"
    autopkgtest "$DSC" -- schroot "$CHROOT"
else
    echo "==> [跳过 pbuilder/autopkgtest（加 --full 跑全量）]"
fi

echo "==> [5/5] 提交并推送 gitea"
git add debian/changelog
git commit -m "debian: bump to ${VERSION}"
git push gitea main 2>/dev/null || git push origin main

echo ""
echo "完成。接下来在本地 mac 执行: ./scripts/debian-2-salsa.sh"
