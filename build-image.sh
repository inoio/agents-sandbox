#!/bin/bash
# Baut das Docker-Image und laedt es in microsandbox.
# Ersetzt das snapshot-basierte setup-microsandbox.sh.
set -euo pipefail

IMAGE_NAME="runner"
SANDBOX_NAME="${SANDBOX_NAME:-workbox}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "=== Docker-Image bauen ==="
docker build \
	-t "$IMAGE_NAME" \
	--build-arg USER_UID="$(id -u)" \
	--build-arg USER_GID="$(id -g)" \
	"$SCRIPT_DIR"

echo ""
echo "=== Image in microsandbox laden ==="
docker save "$IMAGE_NAME" | msb load --tag "$IMAGE_NAME"

echo ""
echo "Fertig. Image '$IMAGE_NAME' in microsandbox verfuegbar."
echo ""
echo "Sandbox starten:"
echo "  ./run-sandbox.sh"
