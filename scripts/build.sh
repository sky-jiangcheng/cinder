#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "=== Cinder Build Script ==="
echo ""

# Step 1: Build frontend
echo "[1/2] Building React frontend..."
cd "$PROJECT_ROOT/web"
npm install --silent
npm run build
echo "  Frontend built to web/dist/"

# Step 2: Build Go binary
echo "[2/2] Building Go binary..."
cd "$PROJECT_ROOT"
export GOPROXY=https://goproxy.cn,direct
go build -ldflags="-s -w" -o cinder .
echo "  Binary: $PROJECT_ROOT/cinder"

echo ""
echo "=== Build complete ==="
ls -lh cinder
