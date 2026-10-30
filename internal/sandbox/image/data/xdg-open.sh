#!/bin/sh
# agents-sandbox xdg-open shim. Installed by agents-sandbox as xdg-open,
# x-www-browser and sensible-browser.
#
# The VM has no desktop, so instead of opening the target here, this hands it
# to the agents-sandbox launcher on the host through a spool directory in the
# home volume. The launcher checks the target against its allowlist and opens
# it with the host's default handler. The exit status follows xdg-open:
# 0 opened, 1 usage error, 3 no launcher answered, 4 refused or failed.
set -u

name=${0##*/}
spool=${AGENTS_SANDBOX_OPEN_SPOOL:-/home/dev/.local/state/agents-sandbox/open}
# The launcher drops requests older than 15 seconds, so stay below that.
timeout_tenths=${AGENTS_SANDBOX_OPEN_TIMEOUT_TENTHS:-80}
max_target_bytes=8192

if [ "$#" -ne 1 ]; then
	echo "usage: $name URL" >&2
	exit 1
fi
case "$1" in
--version)
	echo "$name (agents-sandbox host opener shim)"
	exit 0
	;;
--help | --manual)
	echo "usage: $name URL (opens URL on the host through agents-sandbox)" >&2
	exit 1
	;;
esac
target=$1
if [ "$(printf '%s' "$target" | wc -c)" -gt "$max_target_bytes" ]; then
	echo "$name: target longer than $max_target_bytes bytes" >&2
	exit 1
fi

if ! mkdir -p "$spool" 2>/dev/null; then
	echo "$name: cannot create $spool; open this yourself: $target" >&2
	exit 3
fi
id=$(od -An -N12 -tx1 /dev/urandom | tr -d ' \n')
request="$spool/$id.request"
response="$spool/$id.response"
printf '%s\n' "$target" >"$spool/.$id.tmp" && mv -f "$spool/.$id.tmp" "$request" || exit 3

tenths=0
while [ "$tenths" -lt "$timeout_tenths" ]; do
	if [ -f "$response" ]; then
		IFS= read -r verdict <"$response" || verdict=
		rm -f "$response"
		case "$verdict" in
		ok) exit 0 ;;
		refused*) echo "$name: not opened on the host: ${verdict#refused?}; open this yourself: $target" >&2 ;;
		*) echo "$name: unexpected answer from the host; open this yourself: $target" >&2 ;;
		esac
		exit 4
	fi
	sleep 0.1
	tenths=$((tenths + 1))
done
rm -f "$request"
echo "$name: no answer from the host (opening links needs an attached agents-sandbox launcher with open-links enabled); open this yourself: $target" >&2
exit 3
