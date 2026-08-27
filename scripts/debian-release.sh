#!/usr/bin/env bash
# Debian 打包发版脚本 —— 在 Debian 13 VM 的 /root/nssh 里执行。
# 用法: ./scripts/debian-release.sh <version> [--full]
#   <version>  与上游 tag 对齐的版本号，如 0.27.0
#   --full     跑全量验证（sbuild/lintian/pbuilder/autopkgtest），
#              默认只跑 sbuild+lintian（足够覆盖纯版本号变更）
set -euo pipefail

VERSION="${1:?用法: $0 <version> [--full]}"
FULL="${2:-}"

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
CHROOT="trixie-amd64-sbuild"
PBUILDER_BASE="/var/cache/pbuilder/base-trixie.tgz"
MIRROR="https://mirrors.tuna.tsinghua.edu.cn/debian/"

cd "$REPO_DIR"

echo "==> [1/5] 拉取 gitea 最新代码"
git pull --ff-only

echo "==> [2/5] 更新 debian/changelog 到 ${VERSION}"
dch -v "${VERSION}" "new upstream release"

echo "==> [3/5] 生成源包"
dpkg-buildpackage -S -us -uc -d
DSC="$(dirname "$REPO_DIR")/nssh_${VERSION}.dsc"
[ -f "$DSC" ] || { echo "错误: 未生成 $DSC"; exit 1; }

echo "==> [4/5] sbuild 干净构建"
sbuild --arch=amd64 --dist=trixie --chroot="$CHROOT" "$DSC"

BUILD_LOG="$(ls -t "$(dirname "$REPO_DIR")"/nssh_${VERSION}_amd64*.build | head -1)"
WARNINGS=$(grep -c '^W:' "$BUILD_LOG" || true)
ERRORS=$(grep -c '^E:' "$BUILD_LOG" || true)
echo "    lintian: ${ERRORS} errors, ${WARNINGS} warnings"
if [ "$ERRORS" != "0" ]; then
    grep '^E:' "$BUILD_LOG"
    echo "存在 lintian error，中止"; exit 1
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
echo "完成。接下来在本地 mac 执行: ./scripts/sync-remotes.sh"
