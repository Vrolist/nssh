#!/usr/bin/env bash
# 三远程同步脚本 —— 在本地 mac 的 nssh 仓库执行。
# 用途: VM 推完 gitea 后，把改动拉回本地并同步到 github + salsa。
# 用法: ./scripts/sync-remotes.sh [--push-salsa]
set -euo pipefail

cd "$(cd "$(dirname "$0")/.." && pwd)"

echo "==> [1/2] 从 gitea 拉取"
git fetch gitea
BEHIND=$(git rev-list --count HEAD..gitea/main)
if [ "$BEHIND" = "0" ]; then
    echo "    本地已是最新，无需合并"
else
    git merge --ff-only gitea/main
fi

echo "==> [2/2] 推送 github"
git push origin main

if [ "${1:-}" = "--push-salsa" ]; then
    echo "==> 推送 salsa（触发 Debian 官方 CI）"
    git push salsa main
else
    echo ""
    echo "未推送 salsa。改了 debian/ 或源码时执行: ./scripts/sync-remotes.sh --push-salsa"
fi
