# User dev anlegen
# UID/GID vom Host uebernehmen, damit gemountete Dateien korrekte Ownership haben.
step_check() {
	id dev >/dev/null 2>&1 &&
		[[ "$(id -u dev)" == "$USER_UID" ]] &&
		[[ "$(id -g dev)" == "$USER_GID" ]] &&
		id dev | grep -qw docker &&
		[[ -d /home/dev/.config ]] &&
		[[ -d /home/dev/workspace ]]
}
step_exec() {
	groupadd -g "$USER_GID" dev 2>/dev/null || groupmod -g "$USER_GID" dev 2>/dev/null || true
	useradd -m -u "$USER_UID" -g "$USER_GID" -s /bin/bash dev 2>/dev/null ||
		usermod -u "$USER_UID" -g "$USER_GID" dev 2>/dev/null || true
	usermod -aG docker dev
	mkdir -p /home/dev/.config /home/dev/workspace
	chown -R dev:dev /home/dev
}
