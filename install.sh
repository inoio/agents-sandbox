#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-$HOME/.local}"
BIN_DIR="$INSTALL_DIR/bin"
VENV_DIR="$INSTALL_DIR/share/inoio-sandbox"

echo "==> Installing inoio-sandbox into $VENV_DIR"

python3 -m venv "$VENV_DIR"
"$VENV_DIR/bin/pip" install --upgrade pip
"$VENV_DIR/bin/pip" install .

mkdir -p "$BIN_DIR"
ln -sf "$VENV_DIR/bin/inoio-sandbox" "$BIN_DIR/inoio-sandbox"

if ! command -v msb >/dev/null 2>&1; then
	echo "==> microsandbox (msb) not found"
	echo "    Install from https://github.com/microsandbox/microsandbox"
fi

if ! grep -q "inoio-sandbox" "$HOME/.bashrc" 2>/dev/null; then
	read -p "==> Add 'opencode' alias to ~/.bashrc? [y/N] " reply
	if [[ "$reply" =~ ^[Yy]$ ]]; then
		echo "alias opencode='inoio-sandbox run'" >>"$HOME/.bashrc"
		echo "    Added. Run: source ~/.bashrc"
	fi
fi

echo "==> Done. Add $BIN_DIR to PATH if needed."
