#!/bin/sh
# Syncs the working tree (uncommitted and untracked files included) to each target, which compiles and tests natively
# with `mise run test-host` (mise.toml pins go, claude and codex); a target needs only mise (the docker one: scripts/linux.Dockerfile).
# Targets: local | ssh:HOST | docker:HOST:CONTAINER | win:HOST | wsl:HOST:DISTRO; default: the lines of .test-hosts.
cd "$(git rev-parse --show-toplevel)" || exit 1
logs=${FAV_TEST_ROOT:-$HOME/.cache/fav-test}/logs
mkdir -p "$logs"
[ $# -gt 0 ] || set -- $(grep -v '^#' .test-hosts 2>/dev/null)
[ $# -gt 0 ] || set -- local

pack() {
	git ls-files -co --exclude-standard | while IFS= read -r f; do [ -e "$f" ] && printf '%s\n' "$f"; done |
		COPYFILE_DISABLE=1 tar -czf - --no-xattrs -T -
}

unpack='d=$HOME/.cache/fav-test/src; rm -rf $d && mkdir -p $d && tar -xzf - -C $d 2>/dev/null && cd $d && PATH=$HOME/.local/bin:$PATH && mise trust -q && exec mise run test-host'
docker_path='PATH=$PATH:/usr/local/bin:/opt/homebrew/bin:/Applications/OrbStack.app/Contents/MacOS/xbin'
winsrc='"%LOCALAPPDATA%\fav-test\src"'

run() {
	case $1 in
	local) go run ./tools/test-host ;;
	ssh:*) pack | ssh "${1#ssh:}" "$unpack" ;;
	docker:*)
		r=${1#docker:}
		pack | ssh "${r%%:*}" "$docker_path docker exec -i ${r#*:} sh -c '$unpack'"
		;;
	wsl:*)
		r=${1#wsl:}
		pack | ssh "${r%%:*}" "wsl -d ${r#*:} -e sh -c \"$unpack\""
		;;
	win:*)
		h=${1#win:}
		ssh "$h" "rmdir /s /q $winsrc 2>nul & mkdir $winsrc" &&
			pack | ssh "$h" "tar -xzf - -C $winsrc && cd /d $winsrc && mise trust -q && mise run test-host"
		;;
	*) echo "unknown target $1" && return 2 ;;
	esac
}

start=$(date +%s)
for t in "$@"; do
	log="$logs/$(printf %s "$t" | tr ':/' '__').log"
	run "$t" </dev/null 2>&1 | tr -d '\000' >"$log" &
done
wait
for t in "$@"; do
	log="$logs/$(printf %s "$t" | tr ':/' '__').log"
	printf '%-24s %s\n' "$t" "$(grep 'RESULT ' "$log" | tail -1 | sed 's/.*RESULT //')"
	grep -E '(--- FAIL|FAIL[[:space:]]|panic:|mise ERROR)' "$log" | sed 's/^/    /' | head -20
done
echo "$(($(date +%s) - start))s, logs in $logs"
