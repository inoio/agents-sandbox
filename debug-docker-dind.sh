#!/usr/bin/env bash
# Debugging-Skript: Docker-in-Docker in microsandbox
# Untersucht overlayfs-Fehler und testet Alternativen.
set -euo pipefail

echo "=== 1. Kernel und Filesystem-Support pruefen ==="
msb run docker:dind -- sh -c '
  echo "Kernel: $(uname -r)"
  echo "--- /proc/filesystems (overlay/fuse) ---"
  cat /proc/filesystems | grep -E "overlay|fuse" || echo "kein overlayfs/fuse"
  echo "--- mounts ---"
  mount | head -15
  echo "--- /var/lib/docker backing fs ---"
  df -T /var/lib/docker 2>/dev/null || echo "no /var/lib/docker yet"
'

echo ""
echo "=== 2. Dockerd mit vfs storage driver starten ==="
msb run docker:dind -- sh -c '
  mkdir -p /etc/docker
  echo "{\"storage-driver\":\"vfs\"}" > /etc/docker/daemon.json
  dockerd -H unix:///var/run/docker.sock >/var/log/dockerd.log 2>&1 &
  sleep 5
  echo "--- dockerd status ---"
  DOCKER_HOST=unix:///var/run/docker.sock docker info 2>&1 | head -30
  echo "--- hello-world versuchen ---"
  DOCKER_HOST=unix:///var/run/docker.sock docker run --rm hello-world 2>&1 | head -20
'

echo ""
echo "=== 3. Dockerd-Logs pruefen ==="
msb run docker:dind -- sh -c '
  mkdir -p /etc/docker
  echo "{\"storage-driver\":\"vfs\"}" > /etc/docker/daemon.json
  dockerd -H unix:///var/run/docker.sock >/var/log/dockerd.log 2>&1 &
  sleep 5
  cat /var/log/dockerd.log | tail -30
'

echo ""
echo "=== 4. Alternative: fuse-overlayfs testen ==="
msb run docker:dind -- sh -c '
  echo "--- fuse-overlayfs verfuegbar? ---"
  which fuse-overlayfs 2>/dev/null || echo "nicht installiert"
  echo "--- modprobe overlay ---"
  modprobe overlay 2>&1 || echo "modprobe overlay fehlgeschlagen"
  echo "--- /proc/filesystems nach modprobe ---"
  cat /proc/filesystems | grep overlay || echo "immer noch kein overlayfs"
'

echo ""
echo "=== 5. Alternative: devicemapper storage driver ==="
msb run docker:dind -- sh -c '
  mkdir -p /etc/docker
  echo "{\"storage-driver\":\"devicemapper\"}" > /etc/docker/daemon.json
  dockerd -H unix:///var/run/docker.sock >/var/log/dockerd.log 2>&1 &
  sleep 5
  echo "--- dockerd status ---"
  DOCKER_HOST=unix:///var/run/docker.sock docker info 2>&1 | grep -E "Storage|Server" | head -10
  echo "--- hello-world versuchen ---"
  DOCKER_HOST=unix:///var/run/docker.sock docker run --rm hello-world 2>&1 | head -20
'
