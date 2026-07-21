#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-$HOME/.local}"
BIN_DIR="$INSTALL_DIR/bin"
VENV_DIR="$INSTALL_DIR/share/inoio-sandbox-venv"

echo "==> Installing inoio-sandbox into $VENV_DIR"

if command -v uv >/dev/null 2>&1; then
    uv venv "$VENV_DIR"
    uv pip install --python "$VENV_DIR/bin/python" .
else
    echo "==> uv not found; falling back to python3 venv + pip"
    python3 -m venv "$VENV_DIR"
    "$VENV_DIR/bin/pip" install --upgrade pip
    "$VENV_DIR/bin/pip" install .
fi

mkdir -p "$BIN_DIR"
ln -sf "$VENV_DIR/bin/inoio-sandbox" "$BIN_DIR/inoio-sandbox"

if ! grep -q "inoio-sandbox" "$HOME/.bashrc" 2>/dev/null; then
	add_alias=0
	if [[ "${OPENCODE_ALIAS:-}" == "1" ]]; then
		add_alias=1
	elif [[ "${NONINTERACTIVE:-0}" != "1" && -t 0 ]]; then
		read -p "==> Add 'opencode' alias to ~/.bashrc? [y/N] " reply
		if [[ "$reply" =~ ^[Yy]$ ]]; then
			add_alias=1
		fi
	fi
	if [[ "$add_alias" == "1" ]]; then
		echo "alias opencode='inoio-sandbox run'" >>"$HOME/.bashrc"
		echo "    Added. Run: source ~/.bashrc"
	fi
fi

echo "==> Running post-install checks"
"$BIN_DIR/inoio-sandbox" doctor || echo "    doctor reported issues; see above."

echo "==> Done. Add $BIN_DIR to PATH if needed."
