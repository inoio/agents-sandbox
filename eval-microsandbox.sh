#!/usr/bin/env bash
# Evaluationsskript fuer microsandbox v0.6.6
# Muss ausserhalb von bubblewrap auf dem Host ausgefuehrt werden,
# da /dev/kvm benoetigt wird.
#
# Bereits erfolgreich ausgefuehrte Phasen werden uebersprungen.
# Mit FORCE=1 alle Phasen neu ausfuehren.
set -euo pipefail

REPORT_FILE="${REPORT_FILE:-microsandbox-evaluation-report.md}"
STATE_DIR="${STATE_DIR:-.eval-state}"
PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"

PHASE_FAILED=0
CURRENT_PHASE=""

mkdir -p "$STATE_DIR"

log() {
	echo ""
	echo "=== $1 ==="
}

cmd() {
	local section_file="$STATE_DIR/${CURRENT_PHASE}.md"
	{
		echo "\`\`\`bash"
		echo "$ $1"
	} >>"$section_file"
	if eval "$1" >>"$section_file" 2>&1; then
		:
	else
		local rc=$?
		echo "ERROR: command failed with exit code $rc" >>"$section_file"
		PHASE_FAILED=1
	fi
	{
		echo "\`\`\`"
		echo ""
	} >>"$section_file"
}

phase_done() {
	[[ -f "$STATE_DIR/$1.done" ]]
}

mark_done() {
	touch "$STATE_DIR/$1.done"
}

reset_phase_report() {
	rm -f "$STATE_DIR/$1.md" "$STATE_DIR/$1.done"
}

run_phase() {
	local name="$1"
	local func="$2"

	if [[ "${FORCE:-0}" != "1" ]] && phase_done "$name"; then
		echo "Phase $name bereits erfolgreich - ueberspringe."
		return 0
	fi

	reset_phase_report "$name"
	PHASE_FAILED=0
	CURRENT_PHASE="$name"
	$func
	if [[ "$PHASE_FAILED" -eq 0 ]]; then
		mark_done "$name"
		echo "Phase $name: erfolgreich"
	else
		echo "Phase $name: enthielt Fehler - wird beim naechsten Lauf wiederholt."
	fi
}

assemble_report() {
	{
		echo "# microsandbox v0.6.6 Evaluations-Report"
		echo ""
		echo "Autogeneriert durch \`eval-microsandbox.sh\`."
		echo ""

		for name in phase1 phase2_fs phase2_network phase2_docker phase3_opencode; do
			if [[ -f "$STATE_DIR/$name.md" ]]; then
				cat "$STATE_DIR/$name.md"
			fi
		done
	} >"$REPORT_FILE"
}

phase1() {
	log "Phase 1: Installation & Setup"

	cmd "uname -a"
	cmd "ls -la /dev/kvm"
	cmd "lsmod | grep -i kvm || true"
	cmd "command -v msb"
	cmd "msb --version"
	cmd "msb run debian -- echo 'Hello from microVM'"
}

phase2_fs() {
	log "Phase 2.1: Dateisystem-Isolation"

	cmd "msb run debian -- sh -c 'pwd; ls -la / | head -20'"
	cmd "msb run debian -- sh -c 'touch /tmp/test-write && echo OK || echo BLOCKED'"
	cmd "msb run debian -- sh -c 'ls -la /host 2>&1 || echo no-host-mount'"
	cmd "msb run debian -- sh -c 'touch /root/test-root 2>&1 || echo root-blocked'"
	cmd "msb run debian -- sh -c 'cat /etc/passwd | head -5'"

	# Verhalten bei Host-Mounts testen
	cmd "msb run -v '$PROJECT_DIR:/project:ro' debian -- sh -c 'ls -la /project | head -10'"

	# User-Isolation: als nobody laufen lassen
	cmd "msb run -u nobody debian -- sh -c 'id; touch /root/test-nobody 2>&1 || echo root-blocked-for-nobody'"
}

phase2_network() {
	log "Phase 2.2: Netzwerk / Egress-Kontrolle"

	# alpine hat wget eingebaut, kein apt-get noetig
	# Default-Verhalten (sollte erlaubt sein)
	cmd "msb run alpine -- wget -q -O /dev/null https://example.com && echo example-OK || echo example-FAILED"

	# apt-get funktioniert im debian-Image mit Default-Egress
	cmd "msb run debian -- sh -c 'apt-get update -qq 2>&1 | head -3'"

	# --no-net blockiert alles
	cmd "msb run --no-net alpine -- wget -q -O /dev/null https://example.com 2>&1 && echo example-LEAK || echo example-BLOCKED"

	# --no-net + explizite Allowlist: erlaubter Host funktioniert
	cmd "msb run --no-net --net-rule 'allow@example.com:tcp:443' alpine -- wget -q -O /dev/null https://example.com && echo allowed-OK || echo allowed-BLOCKED"

	# --no-net + Allowlist: nicht-erlaubter Host wird blockiert
	cmd "msb run --no-net --net-rule 'allow@example.com:tcp:443' alpine -- wget -q -O /dev/null https://debian.debian.org 2>&1 && echo non-allowed-LEAK || echo non-allowed-BLOCKED"

	# Secret-Injection: Secret wird nur an erlaubten Host gesendet
	# httpbin.org/headers zeigt die Request-Header an
	export TEST_SECRET="microsandbox-test-secret"
	cmd "msb run --secret 'TEST_SECRET@httpbin.org' alpine -- sh -c 'apk add --no-cache curl >/dev/null 2>&1 && curl -s https://httpbin.org/headers | grep -i test-secret && echo secret-injected || echo secret-missing'"
}

phase2_docker() {
	log "Phase 2.3: Docker-in-Docker"

	cmd "msb run debian -- sh -c 'which docker || echo no-docker'"

	# docker:dind pullen falls noch nicht vorhanden
	if ! msb image ls 2>&1 | grep -q "docker"; then
		echo "HINWEIS: \`docker:dind\` noch nicht gepullt. Pull wird versucht." >>"$STATE_DIR/${CURRENT_PHASE}.md"
		cmd "msb pull docker:dind"
	fi

	# Root-Cause: Docker default overlay2 scheitert am nested-overlay-Mount im microVM-Kernel.
	# Fix: vfs storage driver verwenden (kopiert statt zu mounten).
	cmd "msb run docker:dind -- sh -c 'mkdir -p /etc/docker && echo \"{\\\"storage-driver\\\":\\\"vfs\\\"}\" > /etc/docker/daemon.json && dockerd -H unix:///var/run/docker.sock >/var/log/dockerd.log 2>&1 & sleep 5; DOCKER_HOST=unix:///var/run/docker.sock docker run --rm hello-world 2>&1 | head'"

	# docker info zur Bestaetigung
	cmd "msb run docker:dind -- sh -c 'mkdir -p /etc/docker && echo \"{\\\"storage-driver\\\":\\\"vfs\\\"}\" > /etc/docker/daemon.json && dockerd -H unix:///var/run/docker.sock >/var/log/dockerd.log 2>&1 & sleep 5; DOCKER_HOST=unix:///var/run/docker.sock docker info 2>&1 | grep -E \"Storage|Server Version|Cgroup\"'"
}

phase3_opencode() {
	log "Phase 3: opencode Integration"

	cmd "ls -la ~/.config/opencode 2>&1 || echo no-opencode-config"

	# Host-opencode-Config als Read-Only Mount
	cmd "msb run -v ~/.config/opencode:/home/opencode/.config/opencode:ro debian -- ls -la /home/opencode/.config/opencode 2>&1 || echo mount-failed"

	# Test mit echtem Projekt + git installieren
	cmd "msb run -v '$PROJECT_DIR:/workspace' -w /workspace debian -- sh -c 'ls -la && apt-get update -qq && apt-get install -y -qq git >/dev/null 2>&1 && git status 2>&1 | head'"
}

main() {
	echo "Starte microsandbox Evaluation..."
	echo "Report wird geschrieben nach: $REPORT_FILE"

	run_phase "phase1" phase1
	run_phase "phase2_fs" phase2_fs
	run_phase "phase2_network" phase2_network
	run_phase "phase2_docker" phase2_docker
	run_phase "phase3_opencode" phase3_opencode

	assemble_report

	echo ""
	echo "Evaluation abgeschlossen. Siehe $REPORT_FILE"
}

main "$@"
