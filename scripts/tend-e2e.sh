#!/bin/sh
# End to end over ssh (mode 1): this Mac is the coordinator in a throwaway home; each remote runs its installed fav with
# TEND_HOME / CLAUDE_CONFIG_DIR / CODEX_HOME in a throwaway directory, so no real session or run is touched. Each host
# runs a fake agent to its end, one run is stopped, and the runs' sessions must be listed by `sessions host:<name>`.
# Hosts: mba (macOS), linux (container on mba), wsl and win (lg-win); pass names to run a subset.
# Needs fav installed on every remote (`fav hosts install <name>`) at the same protocol.
set -u
cd "$(git rev-parse --show-toplevel)" || exit 1
id=tend-e2e-$(date +%Y%m%d-%H%M%S)
root=$HOME/.cache/fav-test/e2e/$id
fav=${FAV_BIN:-$HOME/.local/bin/fav}
[ $# -gt 0 ] || set -- mba linux wsl win
mkdir -p "$root/home" "$root/claude" "$root/codex" "$root/bin"
printf '#!/bin/sh\nexit 1\n' >"$root/bin/herdr" && chmod +x "$root/bin/herdr"
export TEND_HOME=$root/home CLAUDE_CONFIG_DIR=$root/claude CODEX_HOME=$root/codex PATH=$root/bin:$PATH
unset HERDR_SOCKET HERDR_PANE_ID HERDR_WORKSPACE_ID FAV_HOME
docker=/Applications/OrbStack.app/Contents/MacOS/xbin/docker
winhome='C:\Users\Administrator'

# host: name ssh-alias remote-root project-dir fav-argv(json)
hostdef() {
	r=/tmp/$id
	envs="\"TEND_HOME=$r/home\", \"CLAUDE_CONFIG_DIR=$r/claude\", \"CODEX_HOME=$r/codex\""
	case $1 in
	mba) printf '%s\n' "mba|$r|$r/proj|[\"env\", $envs, \"/Users/ozn/.local/bin/fav\"]" ;;
	linux) printf '%s\n' "mba|$r|$r/proj|[\"$docker\", \"exec\", \"-i\", \"-e\", \"TEND_HOME=$r/home\", \"-e\", \"CLAUDE_CONFIG_DIR=$r/claude\", \"-e\", \"CODEX_HOME=$r/codex\", \"fav-linux\", \"/root/.local/bin/fav\"]" ;;
	wsl) printf '%s\n' "lg-win|$r|$r/proj|[\"wsl\", \"-d\", \"Debian\", \"-e\", \"env\", $envs, \"/home/admin/.local/bin/fav\"]" ;;
	win) printf '%s\n' "lg-win|$winhome\\$id|$winhome\\$id\\proj|[\"$(printf '%s' "$winhome\\$id\\fav.cmd" | sed 's/\\/\\\\/g')\"]" ;;
	esac
}

prepare() {
	IFS='|' read -r ssh r proj _ <<EOF
$(hostdef "$1")
EOF
	case $1 in
	mba) ssh "$ssh" "mkdir -p $proj" ;;
	linux) ssh "$ssh" "$docker exec fav-linux mkdir -p $proj" ;;
	wsl) ssh "$ssh" "wsl -d Debian -e mkdir -p $proj" ;;
	win)
		printf '@echo off\r\nset "TEND_HOME=%s\\home"\r\nset "CLAUDE_CONFIG_DIR=%s\\claude"\r\nset "CODEX_HOME=%s\\codex"\r\n"%s\\.local\\bin\\fav.exe" %%*\r\n' \
			"$r" "$r" "$r" "$winhome" >"$root/fav.cmd"
		ssh "$ssh" "mkdir $proj" && scp -q "$root/fav.cmd" "$ssh:$id/fav.cmd"
		;;
	esac
}

cleanup() {
	IFS='|' read -r ssh r _ _ <<EOF
$(hostdef "$1")
EOF
	case $1 in
	mba) ssh "$ssh" "rm -rf $r" ;;
	linux) ssh "$ssh" "$docker exec fav-linux rm -rf $r" ;;
	wsl) ssh "$ssh" "wsl -d Debian -e rm -rf $r" ;;
	win) ssh "$ssh" "rmdir /s /q $r" ;;
	esac
}

hosts=""
for h in "$@"; do
	IFS='|' read -r ssh _ _ argv <<EOF
$(hostdef "$h")
EOF
	hosts="$hosts${hosts:+,}{\"name\": \"$h\", \"ssh\": \"$ssh\", \"fav\": $argv}"
	prepare "$h" || { echo "prepare $h failed"; exit 1; }
done
cat >"$TEND_HOME/config.json" <<EOF
{"hosts": [$hosts],
 "agents": [{"name": "quick", "provider": "fake", "args": ["--steps", "2", "--every", "300ms"]},
            {"name": "waiter", "provider": "fake", "args": ["--steps", "1", "--every", "300ms", "--ask"]}]}
EOF

fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got '$2', want '$3'"; fail=$((fail + 1)); fi; }
jq_runs() { "$fav" run list --all --json | python3 -c "import json,sys; rs=json.load(sys.stdin); $1"; }

"$fav" machine list --connect
for h in "$@"; do
	IFS='|' read -r _ _ proj _ <<EOF
$(hostdef "$h")
EOF
	"$fav" task add "e2e on $h" --machine "$h" --agent quick --dir "$proj" --brief "e2e brief for $h" >/dev/null
	t=$("$fav" task list --json | python3 -c "import json,sys; print([t['id'] for t in json.load(sys.stdin) if t['title']=='e2e on $h'][0])")
	"$fav" run start "$t"
done
"$fav" task add "e2e stop on $1" --machine "$1" --agent waiter --dir "$(hostdef "$1" | cut -d'|' -f3)" >/dev/null
stop_task=$("$fav" task list --json | python3 -c "import json,sys; print([t['id'] for t in json.load(sys.stdin) if t['title'].startswith('e2e stop')][0])")
"$fav" run start "$stop_task"

deadline=$(($(date +%s) + 180))
while :; do
	open=$(jq_runs "print(sum(1 for r in rs if r['agent']=='quick' and r['state'] in ('queued','starting','running','unknown')))")
	running=$(jq_runs "print(sum(1 for r in rs if r['agent']=='waiter' and r['state']=='running'))")
	[ "$open" = 0 ] && [ "$running" = 1 ] && break
	[ "$(date +%s)" -gt "$deadline" ] && { echo "timed out"; "$fav" run list --all; break; }
	sleep 3
done
stop_run=$(jq_runs "print([r['id'] for r in rs if r['agent']=='waiter'][0])")
"$fav" run stop "$stop_run"
deadline=$(($(date +%s) + 60))
until [ "$(jq_runs "print([r['state'] for r in rs if r['agent']=='waiter'][0])")" = stopped ] || [ "$(date +%s)" -gt "$deadline" ]; do sleep 3; done

"$fav" run list --all
for h in "$@"; do
	check "$h exited 0" "$(jq_runs "print(' '.join(r['state']+str(r.get('exit_code')) for r in rs if r['machine']=='$h' and r['agent']=='quick'))")" "exited0"
	run=$(jq_runs "print([r['id'] for r in rs if r['machine']=='$h' and r['agent']=='quick'][0])")
	sid=$(jq_runs "print([r.get('session','') for r in rs if r['id']=='$run'][0])")
	check "$h output" "$("$fav" run logs "$run" | grep -c 'fake step')" 2
	check "$h session listed" "$("$fav" sessions "host:$h" turns:0 --json | grep -c "\"session_id\": \"$sid\"")" 1
done
check "stop on $1" "$(jq_runs "print([r['state']+':'+r.get('reason','') for r in rs if r['agent']=='waiter'][0])")" "stopped:asked"

for h in "$@"; do cleanup "$h"; done
echo "$fail failed; coordinator home $root"
[ "$fail" -eq 0 ]
