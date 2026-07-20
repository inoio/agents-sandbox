# Docker installieren und konfigurieren
# Umfasst: GPG-Key, APT-Repo, Docker-Pakete, vfs storage driver.
# vfs ist erforderlich, da Docker default overlay2 am nested-overlay-Mount
# im microVM-Kernel scheitert.
step_check() {
	command -v dockerd >/dev/null 2>&1 &&
		command -v docker >/dev/null 2>&1 &&
		docker buildx version >/dev/null 2>&1 &&
		docker compose version >/dev/null 2>&1 &&
		[[ -f /etc/apt/keyrings/docker.asc ]] &&
		[[ -f /etc/apt/sources.list.d/docker.sources ]] &&
		[[ -f /etc/docker/daemon.json ]] &&
		grep -q '"vfs"' /etc/docker/daemon.json
}
step_exec() {
	# GPG-Key
	install -m 0755 -d /etc/apt/keyrings
	curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc
	chmod a+r /etc/apt/keyrings/docker.asc

	# APT-Repo
	. /etc/os-release
	local arch
	arch=$(dpkg --print-architecture)
	cat >/etc/apt/sources.list.d/docker.sources <<EOF
Types: deb
URIs: https://download.docker.com/linux/debian
Suites: ${VERSION_CODENAME}
Components: stable
Architectures: ${arch}
Signed-By: /etc/apt/keyrings/docker.asc
EOF
	apt-get update

	# Docker-Pakete
	apt-get install -y docker-ce docker-ce-cli containerd.io \
		docker-buildx-plugin docker-compose-plugin

	# vfs storage driver
	mkdir -p /etc/docker
	echo '{"storage-driver":"vfs"}' >/etc/docker/daemon.json
}
