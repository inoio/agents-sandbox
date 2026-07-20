#!/bin/bash

set -euo pipefail

SANDBOX_NAME="builder"
SNAPSHOT_NAME="runner"
STEPS_DIR="$(cd "$(dirname "$0")" && pwd)/setup-steps"

# === Driver-Script: sourced step files in filename order ================
cat >/tmp/sandbox-driver.sh <<'DRIVER'
#!/bin/bash
set -uo pipefail

USER_UID="${1:-$(id -u)}"
USER_GID="${2:-$(id -g)}"
STEPS_DIR="${3:-/tmp/setup-steps}"
CHANGED=0
FAILED_STEPS=""

# Hilfsfunktion: Fuehrt einen Schritt nur aus, wenn die Validierung fehlschlaegt.
# Sammelt Fehler statt abzubrechen — so werden alle Schritte durchlaufen und
# der Snapshot erfasst alle erfolgreichen Schritte.
step() {
	local name="$1" check_fn="$2" exec_fn="$3"
	echo "=== $name ==="
	if "$check_fn" 2>/dev/null; then
		echo "  bereits erfuellt - ueberspringe"
		return 0
	fi
	echo "  nicht erfuellt - fuehre aus..."
	if ! "$exec_fn"; then
		echo "  FEHLER: Ausfuehrung fehlgeschlagen" >&2
		FAILED_STEPS="$FAILED_STEPS $name"
		return 0
	fi
	if ! "$check_fn"; then
		echo "  FEHLER: Validierung nach Ausfuehrung fehlgeschlagen" >&2
		FAILED_STEPS="$FAILED_STEPS $name"
		return 0
	fi
	echo "  OK"
	CHANGED=1
}

# Dateinamen -> Anzeigename: "10_base_packages.sh" -> "base packages"
step_name() {
	local f="$1"
	f="${f##*/}"           # Pfad entfernen
	f="${f#*_}"            # Leading number + underscore entfernen
	f="${f%.sh}"           # .sh entfernen
	f="${f//_/ }"          # Underscores -> Leerzeichen
	echo "$f"
}

# === Schritte ausfuehren (in Dateiname-Reihenfolge) ===
for step_file in "$STEPS_DIR"/*.sh; do
	[[ -f "$step_file" ]] || continue
	name=$(step_name "$step_file")
	# Source-Datei definiert step_check() und step_exec()
	# shellcheck disable=SC1090
	source "$step_file"
	step "$name" step_check step_exec
	unset -f step_check step_exec 2>/dev/null || true
done

echo ""
if [[ -n "$FAILED_STEPS" ]]; then
	echo "WARNUNG: Folgende Schritte sind fehlgeschlagen:$FAILED_STEPS"
	echo "Andere Schritte wurden dennoch ausgefuehrt."
fi

if [[ "$CHANGED" -eq 1 ]]; then
	touch /tmp/.setup-changed
	echo "Setup: Aenderungen vorgenommen - Snapshot muss aktualisiert werden."
else
	echo "Setup: Keine Aenderungen - Snapshot ist aktuell."
fi
echo "Setup abgeschlossen."
DRIVER

# === Host-Ressourcen =======================================================
HOST_CPUS=$(nproc)
HOST_RAM_GIB=$(free -b | awk '/^Mem:/{printf "%.0f\n", $2/(1024*1024*1024)}')
SETUP_MEMORY_GIB=4
SETUP_MAX_MEMORY_GIB=$HOST_RAM_GIB
if ((SETUP_MAX_MEMORY_GIB < SETUP_MEMORY_GIB)); then
	SETUP_MAX_MEMORY_GIB=$SETUP_MEMORY_GIB
fi

# === Sandbox aufräumen (falls vorhanden) ==================================
if msb ls 2>/dev/null | grep -q "$SANDBOX_NAME"; then
	echo "=== Sandbox $SANDBOX_NAME existiert - stoppe und entferne ==="
	msb stop "$SANDBOX_NAME" 2>/dev/null || true
	msb rm "$SANDBOX_NAME" 2>/dev/null || true
fi

# === Basis bestimmen: Snapshot falls vorhanden, sonst frisch =============
SNAPSHOT_EXISTS=0
if msb snapshot ls 2>/dev/null | grep -q "$SNAPSHOT_NAME"; then
	SNAPSHOT_EXISTS=1
fi

if [[ "$SNAPSHOT_EXISTS" -eq 1 ]]; then
	echo "=== Starte Sandbox aus existierendem Snapshot '$SNAPSHOT_NAME' ==="
	msb run \
		--name "$SANDBOX_NAME" \
		--detach \
		-c "$HOST_CPUS" \
		--max-cpus "$HOST_CPUS" \
		-m "${SETUP_MEMORY_GIB}G" \
		--max-memory "${SETUP_MAX_MEMORY_GIB}G" \
		--snapshot "$SNAPSHOT_NAME"
else
	echo "=== Kein Snapshot gefunden - starte frisch von debian ==="
	msb run \
		--name "$SANDBOX_NAME" \
		--detach \
		-c "$HOST_CPUS" \
		--max-cpus "$HOST_CPUS" \
		-m "${SETUP_MEMORY_GIB}G" \
		--max-memory "${SETUP_MAX_MEMORY_GIB}G" \
		debian
fi

# === Driver + Step-Dateien in Sandbox kopieren ============================
echo "=== Kopiere Driver und Step-Dateien ==="
msb cp /tmp/sandbox-driver.sh "${SANDBOX_NAME}:/tmp/sandbox-driver.sh"
msb cp "$STEPS_DIR" "${SANDBOX_NAME}:/tmp/setup-steps"

# === Setup ausfuehren =====================================================
echo "=== Fuehre Setup aus ==="
msb exec "$SANDBOX_NAME" -- bash /tmp/sandbox-driver.sh "$(id -u)" "$(id -g)" /tmp/setup-steps || true

# === Snapshot aktualisieren, falls Aenderungen ===========================
echo "=== Pruefe auf Aenderungen ==="
if msb exec "$SANDBOX_NAME" -- test -f /tmp/.setup-changed 2>/dev/null; then
	CHANGED=0
else
	CHANGED=1
fi

msb stop "$SANDBOX_NAME" 2>/dev/null || true

if [[ "$CHANGED" -eq 0 ]]; then
	echo "=== Aenderungen erkannt - aktualisiere Snapshot '$SNAPSHOT_NAME' ==="
	if [[ "$SNAPSHOT_EXISTS" -eq 1 ]]; then
		msb snapshot rm "$SNAPSHOT_NAME" 2>/dev/null || true
	fi
	msb snapshot create "$SNAPSHOT_NAME" --from "$SANDBOX_NAME"
else
	echo "=== Keine Aenderungen - Snapshot ist aktuell ==="
fi

msb rm "$SANDBOX_NAME" 2>/dev/null || true

echo ""
echo "Fertig. Snapshot '$SNAPSHOT_NAME' ist aktuell."
echo ""
echo "Sandbox starten:"
echo "  ./run-sandbox.sh"
echo ""
echo "Verifikation:"
echo "  msb run --snapshot $SNAPSHOT_NAME -u dev -- id"
echo "  msb run --snapshot $SNAPSHOT_NAME -u dev -v ~/.config/opencode:/home/dev/.config/opencode:ro -- ls /home/dev/.config/opencode"
