# Restore the runner image's composed PATH after a login shell resets it.
#
# /etc/profile on Debian and most distributions replaces PATH with a hard-coded
# default, discarding the entries the image set via ENV (the appended
# /opt/agents-sandbox/bin, project Dockerfile PATH entries, sbin dirs).
# /etc/profile sources /etc/profile.d/*.sh after that reset, so this re-appends
# any AGENTS_SANDBOX_IMAGE_PATH entry missing from PATH, keeping the login
# shell's own entries and their order untouched.
if [ -n "${AGENTS_SANDBOX_IMAGE_PATH:-}" ]; then
  _as_merge_old_ifs=$IFS
  IFS=:
  for _as_merge_dir in $AGENTS_SANDBOX_IMAGE_PATH; do
    case ":$PATH:" in
      *":$_as_merge_dir:"*) ;;
      *) PATH="${PATH:+$PATH:}$_as_merge_dir" ;;
    esac
  done
  IFS=$_as_merge_old_ifs
  export PATH
  unset _as_merge_old_ifs _as_merge_dir
fi
