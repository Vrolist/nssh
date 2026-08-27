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
OUT_DIR="$(dirname "$REPO_DIR")"
CHROOT="trixie-amd64-sbuild"
PBUILDER_BASE="/var/cache/pbuilder/base-trixie.tgz"
MIRROR="https://mirrors.tuna.tsinghua.edu.cn/debian/"

cd "$REPO_DIR"

# 校验是 nssh 仓库（防止误跑在别的目录/仓库副本）
[ -f debian/changelog ] && [ -f main.go ] || {
    echo "错误: $REPO_DIR 不是 nssh 仓库根目录"
    exit 1
}

# 自动探测 gitea 远端名：mac 上叫 gitea，VM 里通常叫 origin。
# 判定标准是 URL 含内网 Gitea 地址或 'gitea' 字样，避免误用 github/salsa 远端
GIT_REMOTE=""
for r in gitea origin; do
    if git remote get-url "$r" >/dev/null 2>&1 \
        && git remote get-url "$r" | grep -q '192.168.2.27\|gitea'; then
        GIT_REMOTE="$r"
        break
    fi
done
[ -n "$GIT_REMOTE" ] || { echo "错误: 未找到 gitea 远端"; git remote -v; exit 1; }

echo "==> [1/5] 拉取 ${GIT_REMOTE} 最新代码"
git pull --ff-only "${GIT_REMOTE}" main

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
DSC="${OUT_DIR}/nssh_${VERSION}.dsc"
[ -f "$DSC" ] || { echo "错误: 未生成 $DSC"; exit 1; }

echo "==> [4/5] sbuild 干净构建"
# 新版 sbuild 支持 --output-dir 指定产物目录；旧版默认产物落盘到当前目录（仓库内），
# 构建后统一清理散落产物。提前探测选项支持，避免真失败时误判重跑
if sbuild --help 2>&1 | grep -q -- '--output-dir'; then
    sbuild --arch=amd64 --dist=trixie --chroot="$CHROOT" \
        --output-dir="$OUT_DIR" "$DSC"
else
    echo "    当前 sbuild 不支持 --output-dir，产物将写入仓库目录（构建后自动清理）"
    sbuild --arch=amd64 --dist=trixie --chroot="$CHROOT" "$DSC"
fi

# 不同 sbuild 版本产物落盘位置不同（dsc 所在目录 或 当前目录），两处都找
BUILD_LOG="$(ls -t "${OUT_DIR}"/nssh_${VERSION}_amd64*.build \
    "${REPO_DIR}"/nssh_${VERSION}_amd64*.build 2>/dev/null | head -1 || true)"
if [ -z "$BUILD_LOG" ]; then
    echo "警告: 未找到 nssh_${VERSION}_amd64*.build，跳过 lintian 结果统计"
    echo "排查: find / -name 'nssh_${VERSION}_amd64*'"
else
    echo "    构建日志: ${BUILD_LOG}"
    WARNINGS=$(grep -c '^W:' "$BUILD_LOG" || true)
    ERRORS=$(grep -c '^E:' "$BUILD_LOG" || true)
    echo "    lintian: ${ERRORS} errors, ${WARNINGS} warnings"
    if [ "$ERRORS" != "0" ]; then
        grep '^E:' "$BUILD_LOG"
        echo "存在 lintian error，中止"
        exit 1
    fi
fi

# 清理仓库目录内散落的二进制产物（旧版 sbuild 行为），保持 git 工作区干净；
# 正式产物统一保留在 $OUT_DIR（/root）
shopt -s nullglob
CLEANUP_COUNT=0
for f in "$REPO_DIR"/nssh_${VERSION}_*.deb \
         "$REPO_DIR"/nssh_${VERSION}_*.buildinfo \
         "$REPO_DIR"/nssh_${VERSION}_*.changes \
         "$REPO_DIR"/nssh_${VERSION}_*-*.build; do
    rm -f "$f"
    CLEANUP_COUNT=$((CLEANUP_COUNT + 1))
done
shopt -u nullglob
if [ "$CLEANUP_COUNT" -gt 0 ]; then
    echo "    已清理仓库内散落产物: ${CLEANUP_COUNT} 个文件"
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

echo "==> [5/5] 提交并推送 ${GIT_REMOTE}"
git add debian/changelog
git commit -m "debian: bump to ${VERSION}"
git push "${GIT_REMOTE}" main

echo ""
echo "完成。接下来在本地 mac 执行: ./scripts/debian-2-salsa.sh --push-salsa"
