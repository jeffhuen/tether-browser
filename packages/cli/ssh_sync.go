package cli

// RemotePrivateCache prepares a private XDG-aware cache without repairing or
// following attacker-controlled directories. It is shared by sync and mirrors.
const RemotePrivateCache = `set -eu
umask 077
cache=${XDG_CACHE_HOME:-$HOME/.cache}
case "$cache" in /*) ;; *) echo 'cache path must be absolute' >&2; exit 1;; esac
uid=$(id -u)
safe_dir() {
[ -d "$1" ] || return 1
owner=$(stat -c %u "$1" 2>/dev/null || stat -f %u "$1")
[ "$owner" = 0 ] || [ "$owner" = "$uid" ] || return 1
mode=$(stat -c %a "$1" 2>/dev/null || stat -f %Lp "$1")
[ "$((0$mode & 022))" -eq 0 ] || [ "$((0$mode & 01000))" -ne 0 ]
}
p=$cache
while :; do
if [ -L "$p" ]; then
owner=$(stat -c %u "$p" 2>/dev/null || stat -f %u "$p")
[ "$owner" = 0 ] || [ "$owner" = "$uid" ] || exit 1
parent=${p%/*}; [ -n "$parent" ] || parent=/
parent=$(cd "$parent" && pwd -P); safe_dir "$parent" || exit 1
q=$(cd "$p" && pwd -P)
while :; do
safe_dir "$q" || exit 1
[ "$q" != / ] || break
q=${q%/*}; [ -n "$q" ] || q=/
done
elif [ -e "$p" ]; then
safe_dir "$p" || exit 1
fi
[ "$p" != / ] || break
p=${p%/*}; [ -n "$p" ] || p=/
done
mkdir -p "$cache"
d=$cache/tether
[ ! -L "$d" ] || exit 1
if [ ! -d "$d" ]; then mkdir -m 700 "$d"; fi
[ -O "$d" ] && [ "$(stat -c %a "$d" 2>/dev/null || stat -f %Lp "$d")" = 700 ] || exit 1
`

// RemoteLoginCommand loads the same login environment as the remote shell.
// Profile output goes to stderr; fd 3 preserves machine-readable command stdout.
// Keeping the body in sh also supports users whose login shell is Fish.
func RemoteLoginCommand(script string) string {
	command := "exec sh -c " + ShellQuote(script) + " 1>&3"
	login := "exec 3>&1; exec \"${SHELL:-/bin/sh}\" -lc " + ShellQuote(command) + " 1>&2"
	return "sh -c " + ShellQuote(login)
}

// TokenSyncCommand contains no key material. Both SSH workflows feed the key
// through a non-TTY stdin channel, then the remote CLI resolves this cache.
func TokenSyncCommand() string {
	return RemoteLoginCommand(RemotePrivateCache + `IFS= read -r token
[ -n "$token" ] || exit 1
[ ! -L "$d/auth" ] || exit 1
[ ! -e "$d/auth" ] || [ -f "$d/auth" ] || exit 1
tmp=$(mktemp "$d/.auth.XXXXXX")
trap 'rm -f "$tmp"' EXIT HUP INT TERM
printf %s "$token" > "$tmp"
unset token
chmod 600 "$tmp"
mv -f "$tmp" "$d/auth"
`)
}
