#!/usr/bin/env bash
# Debian 发版同步脚本（第 2 步，本地 mac） —— 在本地 mac 的 nssh 仓库执行。
# 用途: debian-1-vm.sh 推完 gitea 后，把改动拉回本地并同步到 github + salsa。
# 用法: ./scripts/debian-2-salsa.sh [--push-salsa]
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
