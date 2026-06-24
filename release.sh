#!/bin/bash
set -e

# =====================================================
# nssh Release Script
# Usage: ./release.sh
# =====================================================

GH_REPO="https://github.com/Vrolist/nssh.git"

# =====================================================
# Step 1: Select push target
# =====================================================
echo ""
echo "Select push target:"
echo ""
echo "  1) Gitea only (default)"
echo "  2) Gitea + GitHub"
echo "  0) Cancel"
echo ""
read -p "Enter choice [1-2, 0]: " TARGET_CHOICE

case ${TARGET_CHOICE:-1} in
    1) PUSH_TARGET="gitea" ;;
    2) PUSH_TARGET="both" ;;
    0) echo "Cancelled"; exit 0 ;;
    *) echo "Invalid choice"; exit 1 ;;
esac

echo ""
echo "[INFO] Push target: ${PUSH_TARGET}"

# =====================================================
# Step 2: Get latest tag
# =====================================================
CURRENT_TAG=$(git describe --tags --abbrev=0 2>/dev/null || echo "")

if [ -z "$CURRENT_TAG" ]; then
    CURRENT_VERSION="0.0.0"
    echo "No existing tag found (first release)"
else
    CURRENT_VERSION="${CURRENT_TAG#v}"
    echo "Current version: v${CURRENT_VERSION}"
fi

# Parse version
IFS='.' read -r MAJOR MINOR PATCH <<< "$CURRENT_VERSION"
MAJOR=${MAJOR:-0}
MINOR=${MINOR:-0}
PATCH=${PATCH:-0}

# Calculate candidates
PATCH_VER="${MAJOR}.${MINOR}.$((PATCH + 1))"
MINOR_VER="${MAJOR}.$((MINOR + 1)).0"
MAJOR_VER="$((MAJOR + 1)).0.0"

# =====================================================
# Step 3: Select version
# =====================================================
echo ""
echo "Select release version:"
echo ""
echo "  1) v${PATCH_VER}  (patch - bug fixes)"
echo "  2) v${MINOR_VER}  (minor - new features)"
echo "  3) v${MAJOR_VER}  (major - breaking changes)"
echo "  4) Custom version"
echo "  0) Cancel"
echo ""

read -p "Enter choice [1-4, 0]: " CHOICE

case $CHOICE in
    1) NEW_VERSION="$PATCH_VER" ;;
    2) NEW_VERSION="$MINOR_VER" ;;
    3) NEW_VERSION="$MAJOR_VER" ;;
    4)
        read -p "Enter version (e.g., 2.0.0-beta.1): " NEW_VERSION
        if [ -z "$NEW_VERSION" ]; then
            echo "Version cannot be empty"
            exit 1
        fi
        ;;
    0) echo "Cancelled"; exit 0 ;;
    *) echo "Invalid choice"; exit 1 ;;
esac

TAG="v${NEW_VERSION}"

# Check if tag already exists
if git tag -l "$TAG" | grep -q "$TAG"; then
    echo "Error: tag $TAG already exists"
    exit 1
fi

# Check working tree
DIRTY=$(git status --porcelain)
if [ -n "$DIRTY" ]; then
    echo ""
    echo "Warning: uncommitted changes:"
    echo "$DIRTY"
    echo ""
    read -p "Continue anyway? [y/N]: " CONFIRM
    if [ "$CONFIRM" != "y" ] && [ "$CONFIRM" != "Y" ]; then
        echo "Cancelled"
        exit 1
    fi
fi

# =====================================================
# Step 4: Confirm
# =====================================================
echo ""
echo "========================================"
echo "Release Summary"
echo "========================================"
echo "Version:  $TAG"
echo "Target:   ${PUSH_TARGET}"
echo "Remote:   $(git remote get-url origin 2>/dev/null || echo 'not set')"
echo "Branch:   $(git branch --show-current)"
echo "Commit:   $(git log --oneline -1)"
echo "========================================"
echo ""
echo "Will execute:"
echo "  1. git push origin main"
echo "  2. git tag -a $TAG"
echo "  3. git push origin $TAG"
if [ "$PUSH_TARGET" = "both" ]; then
    echo "  4. git push github HEAD:refs/heads/main --force"
    echo "  5. git push github --tags --force"
fi
echo ""
echo "Gitea Actions will trigger:"
echo "  - CI Test"
echo "  - Release Docker Image -> Harbor"
echo "  - Release Standard Platforms -> MinIO"
if [ "$PUSH_TARGET" = "both" ]; then
    echo ""
    echo "GitHub Actions will trigger:"
    echo "  - CI Test"
    echo "  - Release Docker Image (ghcr.io)"
    echo "  - Release Standard Platforms (GitHub Releases)"
fi
echo ""
read -p "Confirm release? [y/N]: " CONFIRM

if [ "$CONFIRM" != "y" ] && [ "$CONFIRM" != "Y" ]; then
    echo "Cancelled"
    exit 1
fi

# =====================================================
# Step 5: Execute - Gitea
# =====================================================
echo ""
echo "[1/3] Pushing main to Gitea..."
git push origin main

echo "[2/3] Creating tag $TAG..."
git tag -a "$TAG" -m "Release $TAG"

echo "[3/3] Pushing tag $TAG to Gitea..."
git push origin "$TAG"

echo ""
echo "[OK] Gitea released: $TAG"
echo "  Actions: http://192.168.2.27:3000/buladou/nssh/actions"

# =====================================================
# Step 6: Execute - GitHub (if selected)
# =====================================================
if [ "$PUSH_TARGET" = "both" ]; then
    echo ""
    echo "----------------------------------------"
    echo "[GitHub] Pushing to GitHub..."
    echo "----------------------------------------"

    # Ensure github remote is set correctly
    git remote set-url github "${GH_REPO}" 2>/dev/null || \
        git remote add github "${GH_REPO}" 2>/dev/null || true

    echo "[GitHub] [1/2] Pushing main..."
    git push github HEAD:refs/heads/main --force

    echo "[GitHub] [2/2] Pushing tag $TAG..."
    git push github "$TAG" --force

    echo ""
    echo "[OK] GitHub released: $TAG"
    echo "  Actions: https://github.com/Vrolist/nssh/actions"
fi

echo ""
echo "========================================"
echo "Released: $TAG"
echo "========================================"
