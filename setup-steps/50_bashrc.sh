# Shell-Konfiguration fuer dev (.bashrc: direnv + nodenv + opencode)
step_check() {
	grep -q 'direnv hook bash' /home/dev/.bashrc 2>/dev/null &&
		grep -q 'nodenv init' /home/dev/.bashrc 2>/dev/null &&
		grep -q '\.opencode/bin' /home/dev/.bashrc 2>/dev/null
}
step_exec() {
	cat >>/home/dev/.bashrc <<'BASHRC'

# direnv
eval "$(direnv hook bash)"

# nodenv
export PATH="$HOME/.nodenv/bin:$PATH"
eval "$(nodenv init - bash)"

# opencode
export PATH="$HOME/.opencode/bin:$PATH"
BASHRC
	chown dev:dev /home/dev/.bashrc
}
