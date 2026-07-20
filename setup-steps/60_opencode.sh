# opencode installieren (als dev user)
step_check() {
	[[ -x /home/dev/.opencode/bin/opencode ]] &&
		su - dev -c '/home/dev/.opencode/bin/opencode --version' >/dev/null 2>&1
}
step_exec() {
	mkdir -p /var/tmp
	su - dev -c '
		export TMPDIR=/var/tmp
		curl -fsSL https://opencode.ai/install | bash
	'
	if [[ ! -x /home/dev/.opencode/bin/opencode ]]; then
		echo "  WARNUNG: opencode-Binary nicht unter /home/dev/.opencode/bin/" >&2
		find /home/dev -name 'opencode*' -type f 2>/dev/null | head -5
		return 1
	fi
}
