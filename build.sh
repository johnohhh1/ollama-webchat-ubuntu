#!/usr/bin/env bash
set -euo pipefail

OLLAMA_TAG="v0.40.16"
OLLAMA_REPO="https://github.com/ollama/ollama.git"
BUILD_DIR="$(pwd)/build"
OUT_BIN="$BUILD_DIR/ollama-app"

# ── dependency check ────────────────────────────────────────────────────────

need() { command -v "$1" &>/dev/null || { echo "ERROR: '$1' not found. Install it first."; exit 1; }; }
need git
need go
need node
need npm
need pkg-config

pkg-config --exists gtk+-3.0        || { echo "ERROR: libgtk-3-dev not installed."; exit 1; }
pkg-config --exists webkit2gtk-4.1 || { echo "ERROR: libwebkit2gtk-4.1-dev not installed."; exit 1; }
pkg-config --exists ayatana-appindicator3-0.1 || { echo "ERROR: libayatana-appindicator3-dev not installed."; exit 1; }

echo "All dependencies found."

# ── clone ollama ─────────────────────────────────────────────────────────────

SRC="$BUILD_DIR/ollama-src"

if [ -d "$SRC/.git" ]; then
    echo "Source already cloned at $SRC, skipping clone."
else
    echo "Cloning ollama $OLLAMA_TAG ..."
    mkdir -p "$BUILD_DIR"
    git clone --depth 1 --branch "$OLLAMA_TAG" "$OLLAMA_REPO" "$SRC"
fi

# ── apply patch ──────────────────────────────────────────────────────────────

echo "Applying Linux port patch ..."
git -C "$SRC" apply "$(pwd)/patches/linux-port.patch"

# Image generation patches (patches/image-generation*.patch) targeted
# ollama v0.20.x. Upstream removed experimental image generation before
# v0.40, so they are not applied here. See README.

# ── copy new Linux source files ──────────────────────────────────────────────

echo "Copying Linux platform files ..."
cp src/app/cmd/app/app_linux.go        "$SRC/app/cmd/app/"
cp src/app/cmd/app/webview_linux.go    "$SRC/app/cmd/app/"
cp src/app/cmd/app/tray_linux.go       "$SRC/app/cmd/app/"
cp src/app/cmd/app/filepicker_linux.go "$SRC/app/cmd/app/"
cp src/app/cmd/app/desktop_models_linux.go "$SRC/app/cmd/app/"
cp src/app/dialog/dlgs_linux.go        "$SRC/app/dialog/"
cp src/app/server/server_linux.go      "$SRC/app/server/"
cp src/app/updater/updater_linux.go    "$SRC/app/updater/"

# ── build React frontend ─────────────────────────────────────────────────────

echo "Building frontend ..."
cd "$SRC/app/ui/app"
npm install --silent
npm run build
cd -

# ── build Go binary ──────────────────────────────────────────────────────────

echo "Building ollama-app binary ..."
cd "$SRC"
go build -ldflags="-s -w" -o "$OUT_BIN" ./app/cmd/app/
cd -

echo ""
echo "Done! Binary: $OUT_BIN"
echo ""
echo "To install system-wide:"
echo "  sudo cp $OUT_BIN /usr/local/bin/ollama-app"
