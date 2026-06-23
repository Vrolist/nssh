#!/bin/bash
set -e

# =====================================================
# nssh 发布脚本
# 用法: ./release.sh
# =====================================================

# 获取当前最新 tag
CURRENT_TAG=$(git describe --tags --abbrev=0 2>/dev/null || echo "")

if [ -z "$CURRENT_TAG" ]; then
    CURRENT_VERSION="0.0.0"
    echo "当前无版本标签（首次发布）"
else
    CURRENT_VERSION="${CURRENT_TAG#v}"
    echo "当前版本: v${CURRENT_VERSION}"
fi

# 解析版本号
IFS='.' read -r MAJOR MINOR PATCH <<< "$CURRENT_VERSION"
MAJOR=${MAJOR:-0}
MINOR=${MINOR:-0}
PATCH=${PATCH:-0}

# 计算三个候选版本
PATCH_VER="${MAJOR}.${MINOR}.$((PATCH + 1))"
MINOR_VER="${MAJOR}.$((MINOR + 1)).0"
MAJOR_VER="$((MAJOR + 1)).0.0"

echo ""
echo "请选择发布版本:"
echo ""
echo "  1) v${PATCH_VER}  (patch - 小修复)"
echo "  2) v${MINOR_VER}  (minor - 新功能)"
echo "  3) v${MAJOR_VER}  (major - 大版本)"
echo "  4) 手动输入版本号"
echo "  0) 取消"
echo ""

read -p "请输入选项 [1-4, 0]: " CHOICE

case $CHOICE in
    1)
        NEW_VERSION="$PATCH_VER"
        ;;
    2)
        NEW_VERSION="$MINOR_VER"
        ;;
    3)
        NEW_VERSION="$MAJOR_VER"
        ;;
    4)
        read -p "请输入版本号（如 2.0.0-beta.1）: " NEW_VERSION
        if [ -z "$NEW_VERSION" ]; then
            echo "版本号不能为空"
            exit 1
        fi
        ;;
    0)
        echo "已取消"
        exit 0
        ;;
    *)
        echo "无效选项"
        exit 1
        ;;
esac

TAG="v${NEW_VERSION}"

# 检查 tag 是否已存在
if git tag -l "$TAG" | grep -q "$TAG"; then
    echo "错误: tag $TAG 已存在"
    exit 1
fi

# 检查工作区是否干净
DIRTY=$(git status --porcelain)
if [ -n "$DIRTY" ]; then
    echo ""
    echo "警告: 工作区有未提交的更改:"
    echo "$DIRTY"
    echo ""
    read -p "是否继续发布？[y/N]: " CONFIRM
    if [ "$CONFIRM" != "y" ] && [ "$CONFIRM" != "Y" ]; then
        echo "已取消"
        exit 1
    fi
fi

# 确认发布
echo ""
echo "========================================"
echo "发布确认"
echo "========================================"
echo "版本:    $TAG"
echo "远程:    $(git remote get-url origin 2>/dev/null || echo '未设置')"
echo "分支:    $(git branch --show-current)"
echo "最新提交: $(git log --oneline -1)"
echo "========================================"
echo ""
echo "将要执行:"
echo "  1. git tag $TAG"
echo "  2. git push origin main"
echo "  3. git push origin $TAG"
echo ""
echo "Gitea Actions 会自动触发:"
echo "  - CI Test（测试）"
echo "  - Release Docker Image（Docker 推送 Harbor）"
echo "  - Release Standard Platforms（编译 + 推送 GitHub）"
echo ""
read -p "确认发布？[y/N]: " CONFIRM

if [ "$CONFIRM" != "y" ] && [ "$CONFIRM" != "Y" ]; then
    echo "已取消"
    exit 1
fi

# 执行发布
echo ""
echo "推送 main 分支..."
git push origin main

echo "创建 tag $TAG..."
git tag -a "$TAG" -m "Release $TAG"

echo "推送 tag $TAG..."
git push origin "$TAG"

echo ""
echo "========================================"
echo "发布成功: $TAG"
echo "========================================"
echo ""
echo "Gitea Actions 已触发，访问以下链接查看进度:"
echo "  http://192.168.2.27:3000/buladou/nssh/actions"
