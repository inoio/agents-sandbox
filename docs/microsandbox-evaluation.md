# microsandbox: Evaluationsbericht & Setup-Guide

Basierend auf der Evaluation von microsandbox v0.6.6 (Juli 2026).
Vgl. `docs/safe-workspaces-research.de.md` für den Kontext.

---

## TL;DR

microsandbox ist eine MicroVM-basierte Sandbox für CLI-Agenten. Sie bietet Hypervisor-Isolation (KVM/libkrun), präzise Egress-Kontrolle und funktionierendes Docker-in-Docker (mit Konfiguration). Sie eignet sich als Ersatz für Bubblewrap, erfordert aber `/dev/kvm`-Zugriff.

**Funktioniert**: Installation, Hello World, FS-Isolation, Egress-Allowlisting, Docker-in-Docker, Host-Config-Mounts.
**Bekannte Issues**: Install-Skript-Bug (libkrunfw-Version), Secret-Injection unklar, Docker braucht `vfs` storage driver.

---

## 1. Voraussetzungen

| Voraussetzung | Wert |
|---|---|
| OS | Linux (KVM) oder macOS (Apple Silicon, libkrun) |
| KVM | `/dev/kvm` muss existieren, User in `kvm`-Gruppe |
| Kernel-Module | `kvm` + `kvm_amd`/`kvm_intel` geladen |
| libkrunfw | Wird mit msb gebündelt (~21MB), keine System-Installation nötig |
| glibc | ≥ 2.39 (für vorgefertigte Linux-Binaries) |

**Wichtig**: microsandbox funktioniert **nicht** innerhalb von Bubblewrap/Containern ohne `/dev/kvm`-Durchreichung. Die Sandbox muss auf dem Host oder in einer VM mit Nested Virtualization gestartet werden.

### Prerequisites prüfen
```bash
ls -la /dev/kvm              # muss existieren (crw-rw----+ root kvm)
lsmod | grep -i kvm          # kvm + kvm_amd/kvm_intel geladen
id | grep -o kvm             # User in kvm-Gruppe
```

---

## 2. Installation

### Empfohlene Methode
```bash
curl -fsSL https://github.com/superradcompany/microsandbox/releases/download/v0.6.6/install.sh | sh
```

### Bekannter Bug (v0.6.6)
Das offizielle `install.microsandbox.dev`-Skript erwartet `libkrunfw.so.5.6.0`, der Release-Tarball enthält aber `libkrunfw.so.5.5.0`. **Workaround**: Das Release-Asset `install.sh` (GitHub-URL oben) hat die korrekte Version `5.5.0` und funktioniert.

### Manuelle Installation (falls nötig)
```bash
mkdir -p ~/.microsandbox/bin ~/.microsandbox/lib ~/.local/bin
curl -fsSL https://github.com/superradcompany/microsandbox/releases/download/v0.6.6/microsandbox-linux-x86_64.tar.gz -o /tmp/msb.tar.gz
tar -xzf /tmp/msb.tar.gz -C /tmp
install -m 755 /tmp/msb ~/.microsandbox/bin/msb
ln -sf msb ~/.microsandbox/bin/microsandbox
install -m 644 /tmp/libkrunfw.so.5.5.0 ~/.microsandbox/lib/libkrunfw.so.5.5.0
ln -sf libkrunfw.so.5.5.0 ~/.microsandbox/lib/libkrunfw.so.5
ln -sf libkrunfw.so.5 ~/.microsandbox/lib/libkrunfw.so
ln -sf ~/.microsandbox/bin/msb ~/.local/bin/msb
ln -sf ~/.microsandbox/bin/microsandbox ~/.local/bin/microsandbox
```

### Verifizierung
```bash
msb --version              # msb 0.6.6
msb run debian -- echo "Hello from microVM"
```

---

## 3. Dateisystem-Isolation

### Verhalten (Default)
| Test | Ergebnis |
|---|---|
| Eigenes Root-Filesystem | ✅ Sandbox hat isoliertes `/` (overlay) |
| `/tmp` schreibbar | ✅ |
| `/root` als root | ✅ schreibbar (Default-User ist root) |
| `/root` als nobody | ✅ `Permission denied` |
| Host-Home sichtbar? | ❌ Nein (kein automatischer Mount) |
| Host-Pfad mounten | ✅ via `-v SOURCE:DEST[:OPTIONS]` |

### Befehle
```bash
# Projekt read-only mounten
msb run -v "$(pwd):/workspace:ro" debian -- ls -la /workspace

# Projekt read-write mounten + Arbeitsverzeichnis setzen
msb run -v "$(pwd):/workspace" -w /workspace debian -- sh -c 'touch test.txt'

# Als nicht-root User ausführen (empfohlen für Isolation)
msb run -u nobody -v "$(pwd):/workspace:ro" debian -- sh -c 'id'

# Pfade aus dem Guest-Rootfs entfernen
msb run --rm /etc/shadow debian -- sh -c 'cat /etc/shadow 2>&1'
```

---

## 4. Netzwerk / Egress-Kontrolle

microsandbox bietet Host-basierte Netzwerk-Allowlisting. Dies ist die wichtigste Sicherheitsfunktion.

### Verhalten
| Konfiguration | Ergebnis |
|---|---|
| Default (ohne `--no-net`) | ✅ Alle öffentlichen Hosts erreichbar |
| `--no-net` | ❌ Alle Verbindungen blockiert (`bad address`) |
| `--no-net --net-rule 'allow@example.com:tcp:443'` | ✅ example.com erreichbar |
| `--no-net --net-rule 'allow@example.com:tcp:443'` → debian.debian.org | ❌ blockiert |

### Befehle
```bash
# Default (alle öffentlichen Hosts erlaubt)
msb run alpine -- wget -q -O /dev/null https://example.com

# Default-Deny + Allowlist (empfohlen für Produktion)
msb run --no-net \
  --net-rule 'allow@registry.npmjs.org:tcp:443' \
  --net-rule 'allow@github.com:tcp:443' \
  --net-rule 'allow@api.anthropic.com:tcp:443' \
  alpine -- wget -q -O /dev/null https://example.com  # blockiert
```

### Rule-Syntax
```
--net-rule '<action>[:<direction>]@<target>[:<proto>[:<ports>]]'
```
- `<action>`: `allow` oder `deny`
- `<target>`: IP/CIDR, Domain (`example.com`), Suffix (`*.example.com`), Gruppe (`public`, `private`)
- `<proto>`: `tcp` oder `udp`
- `<ports>`: z.B. `443` oder `80,443`

### Secret-Injection
```bash
export MY_API_KEY="sk-..."
msb run --secret 'MY_API_KEY@api.anthropic.com' alpine -- env | grep MY_API_KEY
```
**Status**: Option existiert, aber Injektions-Mechanismus konnte in der Evaluation nicht verifiziert werden (Secret wurde nicht in Request-Headern sichtbar). Möglicherweise ist `--tls-intercept` erforderlich, damit der Proxy Secrets in Requests injiziert.

---

## 5. Docker-in-Docker

### Root Cause des Default-Fehlers
Docker's default `overlay2` storage driver scheitert, weil das Root-Filesystem der Sandbox bereits ein overlay ist (nested overlay mounts werden vom microVM-Kernel 6.12.91 nicht unterstützt). Fehler: `mount source: "overlay" ... err: invalid argument`.

### Fix: `vfs` storage driver
```bash
msb run docker:dind -- sh -c '
  mkdir -p /etc/docker
  echo "{\"storage-driver\":\"vfs\"}" > /etc/docker/daemon.json
  dockerd -H unix:///var/run/docker.sock >/var/log/dockerd.log 2>&1 &
  sleep 5
  DOCKER_HOST=unix:///var/run/docker.sock docker run --rm hello-world
'
```

### Verifiziert
```
Server Version: 29.6.2
Storage Driver: vfs
Cgroup Driver: cgroupfs
Cgroup Version: 2
```

**Trade-off**: `vfs` ist langsamer als `overlay2` (kopiert statt mountet). Für Dev-Workflows akzeptabel, für CI/Production ggf. zu langsam.

---

## 6. opencode Integration

### Host-Config übernehmen
```bash
msb run -v ~/.config/opencode:/home/opencode/.config/opencode:ro debian -- \
  ls -la /home/opencode/.config/opencode
```

### Projekt in Sandbox nutzen
```bash
msb run \
  -v ~/.config/opencode:/home/opencode/.config/opencode:ro \
  -v "$(pwd):/workspace" \
  -w /workspace \
  debian -- sh -c 'ls -la && git status'
```

### Empfohlenes Setup für einen CLI-Agenten
```bash
msb run \
  --name opencode-sandbox \
  -c "$(nproc)" \
  --max-cpus "$(nproc)" \
  -m 4G \
  --max-memory "$(free -b | awk '/^Mem:/{printf "%.0fG", $2/(1024*1024*1024)}')" \
  -v ~/.config/opencode:/home/dev/.config/opencode:ro \
  -v "$(pwd):/workspace" \
  -w /workspace \
  --no-net \
  --net-rule 'allow@api.anthropic.com:tcp:443' \
  --net-rule 'allow@github.com:tcp:443' \
  --net-rule 'allow@registry.npmjs.org:tcp:443' \
  -d \
  debian
```

Danach:
```bash
msb exec opencode-sandbox -- sh -c 'apt-get update -qq && apt-get install -y -qq git nodejs npm'
msb exec -t opencode-sandbox -- opencode
```

Hinweise:
- `-c` / `--max-cpus` nutzt alle Host-CPUs.
- `-m 4G` verhindert OOM-Kills beim Start von opencode; `--max-memory` erlaubt
  Hotplug bis zur gesamten Host-RAM.
- Für interaktive TUI-Apps muss `msb exec -t` verwendet werden, damit die
  Terminal-Größe korrekt übermittelt wird.

---

## 7. Bekannte Limitationen & Issues

| Issue | Status | Workaround |
|---|---|---|
| `install.microsandbox.dev` erwartet falsche libkrunfw-Version | Bug v0.6.6 | Release-Asset `install.sh` von GitHub verwenden |
| Docker `overlay2` schlägt fehl | Kernel-Limit | `vfs` storage driver konfigurieren |
| Secret-Injection nicht verifiziert | Ungeklärt | `--tls-intercept` testen |
| Kein `/dev/kvm` in Bubblewrap | Architektonisch | Auf Host oder VM mit Nested Virtualization ausführen |
| Ephemeral Sandboxes (kein State-Persistenz zwischen `msb run`) | By Design | `-d` (detach) + `msb exec` verwenden, oder Image bauen |

---

## 8. CLI-Referenz

```bash
# Sandbox-Lebenszyklus
msb create --name mybox debian              # Sandbox erstellen + starten
msb exec mybox -- <command>                 # Befehl in laufender Sandbox
msb stop mybox                              # Stoppen
msb start mybox                             # Wieder starten
msb rm mybox                                # Löschen
msb ls                                      # Alle Sandboxes listen

# Einmalige Ausführung
msb run [IMAGE] -- <COMMAND>                # Run + auto-cleanup

# Image-Management
msb pull debian                             # Image pullen
msb image ls                                # Gecachte Images

# Mount-Optionen
-v SOURCE:DEST:ro                           # Read-only Mount
-v SOURCE:DEST                              # Read-write Mount
-w /workspace                               # Working Directory
-u nobody                                   # Als User ausführen

# Netzwerk
--no-net                                    # Alle Verbindungen blockieren
--net-rule 'allow@host:proto:port'          # Allowlist-Eintrag
--secret 'ENV@host'                         # Secret an erlaubten Host
--tls-intercept                              # TLS-Interception (für Secret-Injection)
```

---

## 9. Evaluations-Skripte

- `eval-microsandbox.sh` — Vollständige Evaluation mit Checkpointing (`FORCE=1` für Re-run)
- `debug-docker-dind.sh` — Debugging-Skript für Docker-in-Docker
- `.eval-state/` — State-Verzeichnis (pro Phase eine `.md` + `.done`)
- `microsandbox-evaluation-report.md` — Assemblierter Report
