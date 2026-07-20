# nodenv + node-build installieren (als dev user)
step_check() {
	[[ -x /home/dev/.nodenv/bin/nodenv ]] &&
		[[ -d /home/dev/.nodenv/plugins/node-build ]]
}
step_exec() {
	su - dev -c '
		set -e
		git clone https://github.com/nodenv/nodenv.git ~/.nodenv
		mkdir -p ~/.nodenv/plugins
		git clone https://github.com/nodenv/node-build.git ~/.nodenv/plugins/node-build
	'
	chown -R dev:dev /home/dev/.nodenv
}
