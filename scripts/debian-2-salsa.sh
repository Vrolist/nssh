#!/usr/bin/env bash
# Debian 发版同步脚本（第 2 步，本地 mac） —— 在本地 mac 的 nssh 仓库执行。
# 用途: debian-1-vm.sh 推完 gitea 后，把改动拉回本地并同步到 github + salsa。
# 用法: ./scripts/debian-2-salsa.sh [--push-salsa|--no-salsa]
#   默认交互式询问是否推送 salsa；--push-salsa 跳过询问直接推；
#   --no-salsa 跳过询问直接不推（适合纯脚本同步场景）
set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
SALSA_MODE="ask"

for arg in "$@"; do
    case "$arg" in
        --push-salsa) SALSA_MODE="yes" ;;
        --no-salsa)   SALSA_MODE="no" ;;
        *) echo "未知参数: $arg。用法: $0 [--push-salsa|--no-salsa]"; exit 1 ;;
    esac
done

cd "$REPO_DIR"

# 校验是 nssh 仓库
[ -f debian/changelog ] && [ -f main.go ] || {
    echo "错误: $REPO_DIR 不是 nssh 仓库根目录"
    exit 1
}

# 远端约定: gitea=内网 CI（自动探测，兼容 origin 命名）, origin=github, salsa=Debian 官方
GIT_REMOTE=""
for r in gitea origin; do
    if git remote get-url "$r" >/dev/null 2>&1 \
        && git remote get-url "$r" | grep -q '192.168.2.27\|gitea'; then
        GIT_REMOTE="$r"
        break
    fi
done
[ -n "$GIT_REMOTE" ] || { echo "错误: 未找到 gitea 远端"; git remote -v; exit 1; }
GITHUB_REMOTE="$(git remote | grep -vx "${GIT_REMOTE}" | grep -x 'origin\|github' | head -1 || true)"
[ -n "$GITHUB_REMOTE" ] || GITHUB_REMOTE="origin"

# 检查工作区干净（避免拉取合并时被本地未提交改动干扰）
if ! git diff-index --quiet HEAD --; then
    echo "错误: 本地有未提交改动，先处理后再跑本脚本"
    git status --short
    exit 1
fi

echo "==> [1/2] 从 ${GIT_REMOTE} 拉取"
git fetch "${GIT_REMOTE}"
BEHIND=$(git rev-list --count HEAD.."${GIT_REMOTE}"/main)
AHEAD=$(git rev-list --count "${GIT_REMOTE}"/main..HEAD)
if [ "$AHEAD" != "0" ]; then
    echo "错误: 本地领先 ${GIT_REMOTE}/main ${AHEAD} 个提交，应先推送而不是同步"
    echo "检查是否有未推的 debian-1 产物提交: git log ${GIT_REMOTE}/main..HEAD"
    exit 1
fi
if [ "$BEHIND" = "0" ]; then
    echo "    本地已是最新，无需合并"
else
    git merge --ff-only "${GIT_REMOTE}"/main
fi

echo "==> [2/2] 推送 github (${GITHUB_REMOTE})"
git push "${GITHUB_REMOTE}" main

DEB_VERSION=$(sed -n 's/^nssh (\([^)-]*\)).*/\1/p' debian/changelog | head -1)

if [ "$SALSA_MODE" = "ask" ]; then
    echo ""
    echo "是否推送 salsa？(触发 Debian 官方 CI)，当前 debian/changelog 版本: ${DEB_VERSION}"
    read -p "[y/N]: " ANSWER
    case "${ANSWER:-n}" in
        y|Y) SALSA_MODE="yes" ;;
        *)   SALSA_MODE="no" ;;
    esac
fi

if [ "$SALSA_MODE" = "yes" ]; then
    echo "==> 推送 salsa（触发 Debian 官方 CI），版本: ${DEB_VERSION}"
    git push salsa main
else
    echo ""
    echo "未推送 salsa。之后需要时可执行: ./scripts/debian-2-salsa.sh --push-salsa"
fi

echo ""
echo "完成。后续流程:"
echo "  Salsa CI 查看地址: https://salsa.debian.org/<your-namespace>/nssh/-/pipelines"
