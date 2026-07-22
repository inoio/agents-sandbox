#!/bin/bash
# Launcher: Startet eine Sandbox aus dem lokalen Image mit Env-Whitelist
# und schuetzt .envrc*-Dateien vor Exposition im VM.
set -euo pipefail

IMAGE_NAME="runner"
SANDBOX_NAME="${SANDBOX_NAME:-workbox}"
SANDBOX_MEMORY="${SANDBOX_MEMORY:-4G}"
SANDBOX_CPUS="${SANDBOX_CPUS:-$(nproc)}"
SANDBOX_MAX_CPUS="${SANDBOX_MAX_CPUS:-$SANDBOX_CPUS}"
PROJECT_DIR="$(pwd)"

# === Host-Ressourcen =======================================================
host_ram_gib() {
	free -b | awk '/^Mem:/{printf "%.0f\n", $2/(1024*1024*1024)}'
}

# Groessenangabe wie "4G" oder "512M" in GiB umrechnen
size_to_gib() {
	local size="$1"
	if [[ "$size" =~ ^([0-9]+)G$ ]]; then
		echo "${BASH_REMATCH[1]}"
	elif [[ "$size" =~ ^([0-9]+)M$ ]]; then
		echo $((BASH_REMATCH[1] / 1024))
	elif [[ "$size" =~ ^([0-9]+)$ ]]; then
		echo "$size"
	else
		echo "0"
	fi
}

HOST_RAM_GIB=$(host_ram_gib)
MEMORY_GIB=$(size_to_gib "$SANDBOX_MEMORY")
SANDBOX_MAX_MEMORY_GIB="${SANDBOX_MAX_MEMORY_GIB:-$HOST_RAM_GIB}"
if ((SANDBOX_MAX_MEMORY_GIB < MEMORY_GIB)); then
	SANDBOX_MAX_MEMORY_GIB=$MEMORY_GIB
fi

# === Env-Whitelist ===
# Nur diese Host-Env-Vars werden an die VM weitergereicht.
# Secrets sollten hier NICHT stehen — dafuer --secret verwenden (siehe unten).
ENV_WHITELIST=(
	ANTHROPIC_API_KEY
	GITHUB_TOKEN
	OPENCODE_ZEN_API_KEY
	# Hier weitere Vars ergaenzen
	LITELLM_API_KEY
)

# === VM-spezifische Env-Vars ===
VM_ENV=(
	HOME=/home/dev
	NODE_ENV=development
	SANDBOX_USER=dev
	SHELL=/bin/bash
	PATH=/home/dev/.opencode/bin:/home/dev/.nodenv/bin:/home/dev/.local/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin
)

# === Flags aufbauen ===
FLAGS=(
	--name "$SANDBOX_NAME"
	--replace
	-d
	-c "$SANDBOX_CPUS"
	--max-cpus "$SANDBOX_MAX_CPUS"
	-m "$SANDBOX_MEMORY"
	--max-memory "${SANDBOX_MAX_MEMORY_GIB}G"
	-u dev
	-v "$PROJECT_DIR:/home/dev/workspace"
	-v ~/.config/opencode:/home/dev/.config/opencode:ro
	-w /home/dev/workspace
	-e "LITELLM_API_KEY=sk-MZKWepza1rgGNIVLhP5H3Q"
	-e "OPENCODE_CONFIG_CONTENT={\"provider\":{\"litellm\":{\"npm\":\"@ai-sdk/openai-compatible\",\"name\":\"LiteLLM\",\"options\":{\"apiKey\":\"{env:LITELLM_API_KEY}\"}}}}"
)

# Whitelisted Host-Vars durchreichen (nur wenn gesetzt)
for var in "${ENV_WHITELIST[@]}"; do
	val="${!var:-}"
	if [[ -n "$val" ]]; then
		FLAGS+=(-e "$var=$val")
	fi
done

# VM-spezifische Vars setzen
for env in "${VM_ENV[@]}"; do
	FLAGS+=(-e "$env")
done

# === .envrc*-Dateien vor der VM verbergen ===
# .envrc-Dateien enthalten oft Secrets (API-Keys etc.) und duerfen
# nicht dem Agenten in der VM exponiert werden.
# --rm patcht das Rootfs vor dem Boot; bei Bind-Mounts muss verifiziert
# werden, ob die Dateien tatsaechlich versteckt werden (siehe Verifikation unten).
shopt -s nullglob
for envrc in .envrc*; do
	FLAGS+=(--rm "/home/dev/workspace/$envrc")
done

# === Secret-Injection (optional) ===
# Fuer Secrets, die nur an bestimmte Hosts gesendet werden sollen:
# FLAGS+=(
#   --no-net
#   --net-rule 'allow@api.anthropic.com:tcp:443'
#   --net-rule 'allow@github.com:tcp:443'
#   --secret 'ANTHROPIC_API_KEY@api.anthropic.com'
#   --secret 'GITHUB_TOKEN@github.com'
# )
# Hinweis: --secret benoetigt ggf. --tls-intercept fuer Request-Injektion.

# === Starten ===
msb run "${FLAGS[@]}" "$IMAGE_NAME"

# === Verifikation ===
# Pruefen, ob .envrc wirklich versteckt ist:
msb exec "$SANDBOX_NAME" -u dev -- ls -la /home/dev/workspace/.envrc* 2>/dev/null || true

# Pruefen, welche Env-Vars in der VM sichtbar sind:
msb exec "$SANDBOX_NAME" -u dev -- env | sort

echo ""
echo "=== Sandbox '$SANDBOX_NAME' laeuft im Hintergrund ==="
echo "CPUs: $SANDBOX_CPUS (max: $SANDBOX_MAX_CPUS)"
echo "Speicher: $SANDBOX_MEMORY (max: ${SANDBOX_MAX_MEMORY_GIB}G)"
echo ""
echo "Opencode starten:"
echo "  msb exec -t $SANDBOX_NAME -u dev -- opencode"
echo ""
echo "Falls das Terminal nach einem Absturz Steuerzeichen anzeigt:"
echo "  reset"
echo ""
echo "Sandbox stoppen:"
echo "  msb stop $SANDBOX_NAME && msb rm $SANDBOX_NAME"
