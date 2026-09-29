#!/bin/sh
# End to end over ssh (mode 1): this Mac is the coordinator in a throwaway home; each remote runs its installed tend with
# TEND_HOME / CLAUDE_CONFIG_DIR / CODEX_HOME in a throwaway directory, so no real session or run is touched. Each host
# runs a fake agent to its end (it ends on a question, which a reply continues in the same session), one run is
# stopped, and the runs' sessions must be listed by `sessions host:<name>`. Each host also runs one that waits for a
# permission, which is answered with `run answer` and sent a message with `run send` while it runs, which the agent takes in (seen).
# Hosts: mba (macOS), linux (container on mba), wsl and win (lg-win); pass names to run a subset.
# Needs tend installed on every remote (`tend hosts install <name>`) at the same protocol.
set -u
cd "$(git rev-parse --show-toplevel)" || exit 1
id=tend-e2e-$(date +%Y%m%d-%H%M%S)
root=$HOME/.cache/tend-test/e2e/$id
tend=${TEND_BIN:-$HOME/.local/bin/tend}
[ $# -gt 0 ] || set -- mba linux wsl win
mkdir -p "$root/home" "$root/claude" "$root/codex" "$root/bin"
printf '#!/bin/sh\nexit 1\n' >"$root/bin/herdr" && chmod +x "$root/bin/herdr"
export TEND_HOME=$root/home CLAUDE_CONFIG_DIR=$root/claude CODEX_HOME=$root/codex PATH=$root/bin:$PATH
unset HERDR_SOCKET HERDR_PANE_ID HERDR_WORKSPACE_ID
docker=/Applications/OrbStack.app/Contents/MacOS/xbin/docker
winhome='C:\Users\Administrator'

# host: name ssh-alias remote-root project-dir tend-argv(json)
hostdef() {
	r=/tmp/$id
	envs="\"TEND_HOME=$r/home\", \"CLAUDE_CONFIG_DIR=$r/claude\", \"CODEX_HOME=$r/codex\""
	case $1 in
	mba) printf '%s\n' "mba|$r|$r/proj|[\"env\", $envs, \"/Users/ozn/.local/bin/tend\"]" ;;
	linux) printf '%s\n' "mba|$r|$r/proj|[\"$docker\", \"exec\", \"-i\", \"-e\", \"TEND_HOME=$r/home\", \"-e\", \"CLAUDE_CONFIG_DIR=$r/claude\", \"-e\", \"CODEX_HOME=$r/codex\", \"tend-linux\", \"/root/.local/bin/tend\"]" ;;
	wsl) printf '%s\n' "lg-win|$r|$r/proj|[\"wsl\", \"-d\", \"Debian\", \"-e\", \"env\", $envs, \"/home/admin/.local/bin/tend\"]" ;;
	win) printf '%s\n' "lg-win|$winhome\\$id|$winhome\\$id\\proj|[\"$(printf '%s' "$winhome\\$id\\tend.cmd" | sed 's/\\/\\\\/g')\"]" ;;
	esac
}

prepare() {
	IFS='|' read -r ssh r proj _ <<EOF
$(hostdef "$1")
EOF
	case $1 in
	mba) ssh "$ssh" "mkdir -p $proj" ;;
	linux) ssh "$ssh" "$docker exec tend-linux mkdir -p $proj" ;;
	wsl) ssh "$ssh" "wsl -d Debian -e mkdir -p $proj" ;;
	win)
		printf '@echo off\r\nset "TEND_HOME=%s\\home"\r\nset "CLAUDE_CONFIG_DIR=%s\\claude"\r\nset "CODEX_HOME=%s\\codex"\r\n"%s\\.local\\bin\\tend.exe" %%*\r\n' \
			"$r" "$r" "$r" "$winhome" >"$root/tend.cmd"
		ssh "$ssh" "mkdir $proj" && scp -q "$root/tend.cmd" "$ssh:$id/tend.cmd"
		;;
	esac
}

cleanup() {
	IFS='|' read -r ssh r _ _ <<EOF
$(hostdef "$1")
EOF
	case $1 in
	mba) ssh "$ssh" "rm -rf $r" ;;
	linux) ssh "$ssh" "$docker exec tend-linux rm -rf $r" ;;
	wsl) ssh "$ssh" "wsl -d Debian -e rm -rf $r" ;;
	win) ssh "$ssh" "rmdir /s /q $r" ;;
	esac
}

hosts=""
for h in "$@"; do
	IFS='|' read -r ssh _ _ argv <<EOF
$(hostdef "$h")
EOF
	hosts="$hosts${hosts:+,}{\"name\": \"$h\", \"ssh\": \"$ssh\", \"tend\": $argv}"
	prepare "$h" || { echo "prepare $h failed"; exit 1; }
done
cat >"$TEND_HOME/config.json" <<EOF
{"hosts": [$hosts],
 "agents": [{"name": "quick", "provider": "fake", "args": ["--steps", "2", "--every", "300ms", "--final", "ASK: e2e question?"]},
            {"name": "waiter", "provider": "fake", "args": ["--steps", "1", "--every", "300ms", "--ask"]},
            {"name": "gated", "provider": "fake", "args": ["--steps", "8", "--every", "1s", "--permission", "Bash:e2e deploy"]}]}
EOF

fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got '$2', want '$3'"; fail=$((fail + 1)); fi; }
jq_runs() { "$tend" run list --all --json | python3 -c "import json,sys; rs=json.load(sys.stdin); $1"; }

"$tend" machine list --connect
for h in "$@"; do
	IFS='|' read -r _ _ proj _ <<EOF
$(hostdef "$h")
EOF
	"$tend" task add "e2e on $h" --machine "$h" --agent quick --dir "$proj" --brief "e2e brief for $h" >/dev/null
	t=$("$tend" task list --json | python3 -c "import json,sys; print([t['id'] for t in json.load(sys.stdin) if t['title']=='e2e on $h'][0])")
	"$tend" run start "$t"
done
"$tend" task add "e2e stop on $1" --machine "$1" --agent waiter --dir "$(hostdef "$1" | cut -d'|' -f3)" >/dev/null
stop_task=$("$tend" task list --json | python3 -c "import json,sys; print([t['id'] for t in json.load(sys.stdin) if t['title'].startswith('e2e stop')][0])")
"$tend" run start "$stop_task"

deadline=$(($(date +%s) + 180))
while :; do
	open=$(jq_runs "print(sum(1 for r in rs if r['agent']=='quick' and r['state'] in ('queued','starting','running','unknown')))")
	running=$(jq_runs "print(sum(1 for r in rs if r['agent']=='waiter' and r['state']=='running'))")
	[ "$open" = 0 ] && [ "$running" = 1 ] && break
	[ "$(date +%s)" -gt "$deadline" ] && { echo "timed out"; "$tend" run list --all; break; }
	sleep 3
done
stop_run=$(jq_runs "print([r['id'] for r in rs if r['agent']=='waiter'][0])")
"$tend" run stop "$stop_run"
deadline=$(($(date +%s) + 60))
until [ "$(jq_runs "print([r['state'] for r in rs if r['agent']=='waiter'][0])")" = stopped ] || [ "$(date +%s)" -gt "$deadline" ]; do sleep 3; done

"$tend" run list --all
for h in "$@"; do
	check "$h exited 0" "$(jq_runs "print(' '.join(r['state']+str(r.get('exit_code')) for r in rs if r['machine']=='$h' and r['agent']=='quick'))")" "exited0"
	run=$(jq_runs "print([r['id'] for r in rs if r['machine']=='$h' and r['agent']=='quick'][0])")
	sid=$(jq_runs "print([r.get('session','') for r in rs if r['id']=='$run'][0])")
	check "$h output" "$("$tend" run logs "$run" | grep -c '"text":"fake step')" 2
	check "$h session listed" "$("$tend" sessions "host:$h" turns:0 --json | grep -c "\"session_id\": \"$sid\"")" 1
	check "$h waits" "$(jq_runs "print([r.get('attention','')+':'+r.get('ask','') for r in rs if r['id']=='$run'][0])")" "asked:e2e question?"
	check "$h agents checked" "$("$tend" machine list --connect --json | python3 -c "import json,sys; print(sorted([m for m in json.load(sys.stdin) if m['name']=='$h'][0].get('agents',{})))")" "['claude', 'codex']"
	"$tend" run continue "$run" "e2e reply on $h" >/dev/null
done
deadline=$(($(date +%s) + 120))
until [ "$(jq_runs "print(sum(1 for r in rs if r.get('parent') and r['state'] in ('queued','starting','running','unknown')))")" = 0 ] || [ "$(date +%s)" -gt "$deadline" ]; do sleep 3; done
for h in "$@"; do
	first=$(jq_runs "print([r['id'] for r in rs if r['machine']=='$h' and r['agent']=='quick' and not r.get('parent')][0])")
	check "$h reply continues the session" "$(jq_runs "print(' '.join(r['state']+str(r.get('exit_code'))+(':same' if r.get('session')==[x for x in rs if x['id']=='$first'][0].get('session') else ':other') for r in rs if r.get('parent')=='$first'))")" "exited0:same"
done
check "stop on $1" "$(jq_runs "print([r['state']+':'+r.get('reason','') for r in rs if r['agent']=='waiter'][0])")" "stopped:asked"

for h in "$@"; do
	"$tend" task add "e2e gated on $h" --machine "$h" --agent gated --dir "$(hostdef "$h" | cut -d'|' -f3)" >/dev/null
	t=$("$tend" task list --json | python3 -c "import json,sys; print([t['id'] for t in json.load(sys.stdin) if t['title']=='e2e gated on $h'][0])")
	"$tend" run start "$t" >/dev/null
done
answered=""
deadline=$(($(date +%s) + 180))
while [ "$(jq_runs "print(sum(1 for r in rs if r['agent']=='gated' and r['state'] in ('queued','starting','running','unknown')))")" != 0 ]; do
	for run in $(jq_runs "print(' '.join(r['id']+':'+r['machine'] for r in rs if r['agent']=='gated' and r.get('requests')))"); do
		case " $answered " in *" ${run%%:*} "*) continue ;; esac
		"$tend" run answer "${run%%:*}" --allow >/dev/null && "$tend" run send "${run%%:*}" "e2e message on ${run#*:}" >/dev/null
		answered="$answered ${run%%:*}"
	done
	[ "$(date +%s)" -gt "$deadline" ] && { echo "gated runs timed out"; "$tend" run list --all; break; }
	sleep 2
done
for h in "$@"; do
	run=$(jq_runs "print([r['id'] for r in rs if r['machine']=='$h' and r['agent']=='gated'][0])")
	check "$h permission answered" "$(jq_runs "print([r['state']+str(r.get('exit_code'))+':'+','.join(m['state'] for m in r.get('sends') or []) for r in rs if r['id']=='$run'][0])")" "exited0:seen"
	check "$h agent heard it" "$("$tend" run logs "$run" | grep -c -e 'allowed Bash' -e "heard: e2e message on $h")" 2
done

for h in "$@"; do cleanup "$h"; done
echo "$fail failed; coordinator home $root"
[ "$fail" -eq 0 ]
