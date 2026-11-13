#!/usr/bin/env bash
# Install the optional image generation runner as a systemd user service.
# Needs an NVIDIA GPU. Downloads ~3 GB of Python packages now; models
# (~25-35 GB each) download from Hugging Face on first use.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
DEST="$HOME/.local/share/ollama-imagegen"
VENV="$DEST/venv"
CUDA_INDEX="${CUDA_INDEX:-https://download.pytorch.org/whl/cu130}"

command -v uv &>/dev/null || { echo "ERROR: uv not found. Install it: curl -LsSf https://astral.sh/uv/install.sh | sh"; exit 1; }
command -v nvidia-smi &>/dev/null || echo "WARNING: nvidia-smi not found; image generation needs an NVIDIA GPU."

mkdir -p "$DEST"
if [ ! -x "$VENV/bin/python" ]; then
    echo "Creating venv at $VENV ..."
    uv venv -q --python 3.12 "$VENV"
fi
echo "Installing torch ($CUDA_INDEX) and diffusers ..."
uv pip install -q --python "$VENV/bin/python" torch --index-url "$CUDA_INDEX"
uv pip install -q --python "$VENV/bin/python" diffusers transformers accelerate safetensors sentencepiece

cp "$HERE/imagegen_server.py" "$DEST/"
mkdir -p "$HOME/.config/systemd/user"
cp "$HERE/ollama-imagegen.service" "$HOME/.config/systemd/user/"
systemctl --user daemon-reload
systemctl --user enable ollama-imagegen.service
systemctl --user restart ollama-imagegen.service

echo ""
echo "Runner installed and started. Logs: journalctl --user -u ollama-imagegen -f"
echo "Models: ~/.config/ollama-imagegen/models.json"
