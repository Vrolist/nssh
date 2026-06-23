#!/bin/bash
set -e

# =====================================================
# nssh Release Script
# Usage:
#   ./release.sh              Create and push a new tag to Gitea
#   ./release.sh --check TAG  Check Gitea Actions status for TAG
#   ./release.sh --help       Show help
# =====================================================

GITEA_SERVER="http://192.168.2.27:3000"
GITEA_OWNER="buladou"
GITEA_REPO="nssh"
GH_REPO="https://github.com/Vrolist/nssh.git"
CONFIG_FILE="$HOME/.nssh-release"

# Load saved config
if [ -f "$CONFIG_FILE" ]; then
  source "$CONFIG_FILE"
fi

show_help() {
  echo "nssh Release Script"
  echo ""
  echo "Usage:"
  echo "  ./release.sh              Create and push a new tag to Gitea"
  echo "  ./release.sh --check TAG  Check Gitea Actions status for TAG"
  echo "  ./release.sh --help       Show this help"
  echo ""
  echo "Examples:"
  echo "  ./release.sh"
  echo "  ./release.sh --check v0.17.4"
  echo ""
  echo "After Gitea Actions pass, push to GitHub with:"
  echo "  git remote add github ${GH_REPO}"
  echo "  git push github HEAD:refs/heads/main --force"
  echo "  git push github --tags --force"
  exit 0
}

# =====================================================
# Check Gitea Actions status for a given tag
# =====================================================
check_status() {
  local TAG="$1"

  if [ -z "$TAG" ]; then
    echo "Error: TAG required. Usage: ./release.sh --check TAG"
    exit 1
  fi

  # Get token
  if [ -z "$GITEA_TOKEN" ]; then
    read -p "Gitea Token (from http://192.168.2.27:3000/user/settings/applications): " GITEA_TOKEN
    if [ -z "$GITEA_TOKEN" ]; then
      echo "Error: Token required"
      exit 1
    fi
    echo "GITEA_TOKEN=$GITEA_TOKEN" >> "$CONFIG_FILE"
    chmod 600 "$CONFIG_FILE"
  fi

  echo "Checking Gitea Actions runs for ${TAG}..."
  echo ""

  # Fetch workflow runs for this tag
  RESPONSE=$(curl -s -H "Authorization: token ${GITEA_TOKEN}" \
    "${GITEA_SERVER}/api/v1/repos/${GITEA_OWNER}/${GITEA_REPO}/actions/runs?ref=${TAG}&limit=10")

  TOTAL=$(echo "$RESPONSE" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('total_count', 0))" 2>/dev/null || echo "0")

  if [ "$TOTAL" -eq 0 ]; then
    echo "No workflow runs found for ${TAG}."
    echo "Possible reasons:"
    echo "  - The tag was pushed but workflows haven't started yet"
    echo "  - The Gitea Actions runner is not running"
    echo ""
    echo "Visit: ${GITEA_SERVER}/${GITEA_OWNER}/${GITEA_REPO}/actions"
    exit 1
  fi

  echo "Found ${TOTAL} workflow run(s):"
  echo ""

  ALL_PASSED=true
  ANY_RUNNING=false
  echo "  $(printf '%-35s %-12s %-12s' 'WORKFLOW' 'STATUS' 'CONCLUSION')"
  echo "  $(printf '%s' '----------------------------------- ------------- -------------')"

  for row in $(python3 -c "
import json, sys
d = json.load(sys.stdin)
for r in d.get('workflow_runs', []):
    name = r.get('workflow_name', 'unknown')
    status = r.get('status', 'unknown')
    conclusion = r.get('conclusion', '') or 'incomplete'
    print(f\"{name}|{status}|{conclusion}\")
" 2>/dev/null <<< "$RESPONSE"); do
    IFS='|' read -r NAME STATUS CONCLUSION <<< "$row"
    printf "  %-35s %-12s %-12s\n" "$NAME" "$STATUS" "$CONCLUSION"

    if [ "$STATUS" != "success" ] && [ "$CONCLUSION" != "success" ]; then
      ALL_PASSED=false
    fi
    if [ "$STATUS" = "running" ] || [ "$STATUS" = "waiting" ]; then
      ANY_RUNNING=true
    fi
  done

  echo ""

  if $ANY_RUNNING; then
    echo "⚠️  Some workflows are still running."
    echo "   Re-run this check later: $0 --check ${TAG}"
    exit 1
  fi

  if $ALL_PASSED; then
    echo "============================================"
    echo "  All Gitea Actions passed for ${TAG}!"
    echo "  Ready to push to GitHub."
    echo "============================================"
    echo ""
    echo "Run the following commands:"
    echo ""
    echo "  cd $(pwd)"
    echo "  git remote add github ${GH_REPO}"
    echo "  git push github HEAD:refs/heads/main --force"
    echo "  git push github --tags --force"
    echo ""
    echo "After pushing, GitHub Actions will trigger automatically:"
    echo "  - CI Test"
    echo "  - Release Docker Image (ghcr.io)"
    echo "  - Release Standard Platforms (GitHub Releases)"
    echo ""

    read -p "Push to GitHub now? [y/N]: " CONFIRM
    if [ "$CONFIRM" = "y" ] || [ "$CONFIRM" = "Y" ]; then
      git remote add github "${GH_REPO}" 2>/dev/null || true
      git push github HEAD:refs/heads/main --force
      git push github --tags --force
      echo ""
      echo "============================================"
      echo "  Pushed to GitHub: ${TAG}"
      echo "============================================"
    fi
  else
    echo "============================================"
    echo "  ❌ Some workflows failed for ${TAG}"
    echo "============================================"
    echo ""
    echo "Check the logs: ${GITEA_SERVER}/${GITEA_OWNER}/${GITEA_REPO}/actions"
    echo ""
    read -p "Create a new patch version to fix? [y/N]: " CONFIRM
    if [ "$CONFIRM" = "y" ] || [ "$CONFIRM" = "Y" ]; then
      # Parse current version and bump patch
      CURRENT="${TAG#v}"
      IFS='.' read -r MAJOR MINOR PATCH <<< "$CURRENT"
      NEW_VER="${MAJOR}.${MINOR}.$((PATCH + 1))"
      echo "Creating v${NEW_VER}..."
      git tag -a "v${NEW_VER}" -m "Release v${NEW_VER}"
      git push origin "v${NEW_VER}"
      echo "Pushed v${NEW_VER} to Gitea"
    fi
  fi
}

# =====================================================
# Create a new release tag
# =====================================================
create_release() {
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
    1) NEW_VERSION="$PATCH_VER" ;;
    2) NEW_VERSION="$MINOR_VER" ;;
    3) NEW_VERSION="$MAJOR_VER" ;;
    4)
      read -p "Enter version (e.g., 2.0.0-beta.1): " NEW_VERSION
      if [ -z "$NEW_VERSION" ]; then
        echo "Version cannot be empty"; exit 1
      fi
      ;;
    0) echo "Cancelled"; exit 0 ;;
    *) echo "Invalid choice"; exit 1 ;;
  esac

  TAG="v${NEW_VERSION}"

  # Check if tag exists
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
      echo "Cancelled"; exit 1
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
  echo "Gitea Actions will trigger:"
  echo "  - CI Test"
  echo "  - Release Docker Image -> Harbor"
  echo "  - Release Standard Platforms -> MinIO"
  echo ""
  echo "After Gitea Actions pass, check and push to GitHub:"
  echo "  $0 --check ${TAG}"
  echo ""
  read -p "Confirm release? [y/N]: " CONFIRM

  if [ "$CONFIRM" != "y" ] && [ "$CONFIRM" != "Y" ]; then
    echo "Cancelled"; exit 1
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
  echo ""
  echo "Next step: wait for Gitea Actions to complete, then run:"
  echo "  $0 --check ${TAG}"
}

# =====================================================
# Main
# =====================================================
case "${1:-}" in
  --help|-h)
    show_help
    ;;
  --check)
    check_status "${2:-}"
    ;;
  "")
    create_release
    ;;
  *)
    echo "Unknown option: $1"
    echo "Usage: $0 [--check TAG] [--help]"
    exit 1
    ;;
esac
