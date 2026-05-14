#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TAURI_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
REPO_ROOT="$(cd "$TAURI_DIR/.." && pwd)"
TARGET_DIR="$TAURI_DIR/src-tauri/target/release"
PORTABLE_DIR="$REPO_ROOT/dist/desktop/windows/portable"

require_file() {
  local path="$1"
  if [ ! -f "$path" ]; then
    echo "error: missing expected build artifact: $path" >&2
    exit 1
  fi
}

main() {
  (
    cd "$TAURI_DIR"
    bash "$SCRIPT_DIR/run-tauri.sh" build --no-bundle
  )

  local app sidecar loader
  app="$TARGET_DIR/agentsview-desktop.exe"
  sidecar="$TARGET_DIR/agentsview-backend.exe"
  loader="$TARGET_DIR/WebView2Loader.dll"

  require_file "$app"
  require_file "$sidecar"
  require_file "$loader"

  mkdir -p "$PORTABLE_DIR"
  cp "$app" "$PORTABLE_DIR/agentsview-desktop.exe"
  cp "$app" "$PORTABLE_DIR/agentsview.exe"
  cp "$sidecar" "$PORTABLE_DIR/agentsview-backend.exe"
  cp "$loader" "$PORTABLE_DIR/WebView2Loader.dll"

  echo "Synced portable desktop build to $PORTABLE_DIR"
}

main "$@"
