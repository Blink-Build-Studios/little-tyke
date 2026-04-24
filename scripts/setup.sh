#!/usr/bin/env bash
set -euo pipefail

echo "=== little-tyke setup ==="

# Check for Ollama
if ! command -v ollama &>/dev/null; then
    echo "Ollama not found. Installing..."
    if [[ "$(uname)" == "Darwin" ]]; then
        brew install ollama
    else
        curl -fsSL https://ollama.ai/install.sh | sh
    fi
else
    echo "Ollama already installed: $(ollama --version)"
fi

# Check if Ollama is running
if ! curl -sf http://localhost:11434/api/tags &>/dev/null; then
    echo ""
    echo "Ollama is installed but not running."
    echo "Start it with: ollama serve"
    echo ""
fi

# Build little-tyke
echo "Building little-tyke..."
make build

echo ""
echo "Setup complete! Run with: make run"
