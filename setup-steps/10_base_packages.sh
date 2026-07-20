# Basis-Pakete
step_check() {
	local pkg
	for pkg in ca-certificates curl git direnv; do
		dpkg -l "$pkg" 2>/dev/null | grep -q "^ii.*$pkg" || return 1
	done
}
step_exec() {
	apt-get update
	apt-get install -y ca-certificates curl git direnv
}
