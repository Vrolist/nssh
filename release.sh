#!/bin/bash
set -e

# =====================================================
# nssh Release Script
# Usage: ./release.sh
# =====================================================

# Get latest tag
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
        read -p "Enter version (e.g., 2.0.0-beta.1): " NEW_VERSION
        if [ -z "$NEW_VERSION" ]; then
            echo "Version cannot be empty"
            exit 1
        fi
        ;;
    0)
        echo "Cancelled"
        exit 0
        ;;
    *)
        echo "Invalid choice"
        exit 1
        ;;
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

# Confirm
echo ""
echo "========================================"
echo "Release Summary"
echo "========================================"
echo "Version:  $TAG"
echo "Remote:   $(git remote get-url origin 2>/dev/null || echo 'not set')"
echo "Branch:   $(git branch --show-current)"
echo "Commit:   $(git log --oneline -1)"
echo "========================================"
echo ""
echo "Will execute:"
echo "  1. git push origin main"
echo "  2. git tag -a $TAG"
echo "  3. git push origin $TAG"
echo ""
echo "Gitea Actions will trigger automatically:"
echo "  - CI Test"
echo "  - Release Docker Image -> Harbor"
echo "  - Release Standard Platforms -> GitHub"
echo ""
read -p "Confirm release? [y/N]: " CONFIRM

if [ "$CONFIRM" != "y" ] && [ "$CONFIRM" != "Y" ]; then
    echo "Cancelled"
    exit 1
fi

# Execute
echo ""
echo "Pushing main..."
git push origin main

echo "Creating tag $TAG..."
git tag -a "$TAG" -m "Release $TAG"

echo "Pushing tag $TAG..."
git push origin "$TAG"

echo ""
echo "========================================"
echo "Released: $TAG"
echo "========================================"
