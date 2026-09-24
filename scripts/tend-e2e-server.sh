#!/bin/sh
# End to end in mode 2: `fav server` in the fav-linux container on mba (a forwarder on mba carries mba's tailnet
# address to it), nodes on mba, the container itself and win dialing in with tokens, and this Mac as a client.
# ⚠️ The tailnet ACL lets lg-win open no connection to mba: win dials through `ssh -R` from here to its loopback. WSL
# (NAT) cannot reach that loopback, so it is left out unless named (`wsl` needs lg-win to reach mba:$port directly).
# Every home is a throwaway directory; every process is an ssh session from here or is killed at the end.
# Checks: each node runs a fake agent to exit 0 and its output reaches the client; a wrong token gets 401; after the
# server restarts, every node is back and another run goes through.
set -u
cd "$(git rev-parse --show-toplevel)" || exit 1
id=tend-e2e-srv-$(date +%Y%m%d-%H%M%S)
root=$HOME/.cache/fav-test/e2e/$id
fav=${FAV_BIN:-$HOME/.local/bin/fav}
tailnet=100.101.8.10
port=7788
url=ws://$tailnet:$port
docker=/Applications/OrbStack.app/Contents/MacOS/xbin/docker
winhome='C:\Users\Administrator'
r=/tmp/$id
mkdir -p "$root/home" "$root/claude" "$root/codex" "$root/bin"
printf '#!/bin/sh\nexit 1\n' >"$root/bin/herdr" && chmod +x "$root/bin/herdr"
export TEND_HOME=$root/home CLAUDE_CONFIG_DIR=$root/claude CODEX_HOME=$root/codex PATH=$root/bin:$PATH
unset HERDR_SOCKET HERDR_PANE_ID HERDR_WORKSPACE_ID FAV_HOME
pids=""
bg() { "$@" </dev/null >>"$root/bg.log" 2>&1 & pids="$pids $!"; }
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got '$2', want '$3'"; fail=$((fail + 1)); fi; }
envs() { printf 'TEND_HOME=%s/%s/home CLAUDE_CONFIG_DIR=%s/%s/claude CODEX_HOME=%s/%s/codex' "$r" "$1" "$r" "$1" "$r" "$1"; }
cexec() { ssh mba "$docker exec -i fav-linux $*"; }

server_home=$r/server
cexec mkdir -p "$server_home"
token() { cexec env TEND_HOME=$server_home /root/.local/bin/fav server token add "$@" 2>/dev/null; }
[ $# -gt 0 ] || set -- mba linux win
nodes=$#
for h in "$@"; do eval "tok_$h=\$(token --node $h)"; done
tok_me=$(token --client mac)
[ -n "$tok_me" ] || { echo "token add failed"; exit 1; }
# ckill PATTERN: the container has no pkill / kill binary; the shell's kill and /proc do
ckill() { # the first character in brackets keeps the pattern from matching this shell's own command line
	pat="[$(printf %s "$1" | cut -c1)]$(printf %s "$1" | cut -c2-)"
	ssh mba "$docker exec fav-linux sh -c 'for p in /proc/[0-9]*; do tr \"\\000\" \" \" <\$p/cmdline 2>/dev/null | grep -q -- \"$pat\" && kill \${p#/proc/}; done; true'"
}
# the server records its pid in its throwaway home: only this test's server is ever stopped
start_server() { bg ssh mba "$docker exec -i fav-linux sh -c 'echo \$\$ >$server_home/pid; exec env TEND_HOME=$server_home /root/.local/bin/fav server --listen 0.0.0.0:$port --plain'"; }
stop_server() { ssh mba "$docker exec fav-linux sh -c 'kill \$(cat $server_home/pid)'"; }
start_server
ctr_ip=$(ssh mba "$docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' fav-linux")
cat >"$root/fwd.py" <<'EOF'
import socket, sys, threading
lh, lp, th, tp = sys.argv[1], int(sys.argv[2]), sys.argv[3], int(sys.argv[4])
def pipe(a, b):
    try:
        while True:
            d = a.recv(65536)
            if not d:
                break
            b.sendall(d)
    except OSError:
        pass
    finally:
        for s in (a, b):
            try:
                s.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind((lh, lp))
s.listen(64)
while True:
    c, _ = s.accept()
    try:
        u = socket.create_connection((th, tp))
    except OSError:
        c.close()
        continue
    threading.Thread(target=pipe, args=(c, u), daemon=True).start()
    threading.Thread(target=pipe, args=(u, c), daemon=True).start()
EOF
ssh mba "mkdir -p $r" && scp -q "$root/fwd.py" "mba:$r/fwd.py"
bg ssh mba "exec python3 $r/fwd.py $tailnet $port $ctr_ip $port"
sleep 2
check "healthz through the tailnet" "$(curl -s -m 5 http://$tailnet:$port/healthz)" ok
check "a wrong token gets 401" "$(curl -s -m 5 -o /dev/null -w '%{http_code}' -H 'Authorization: Bearer tend_nope' http://$tailnet:$port/client)" 401

# nodes: a throwaway home with node.allow_dirs, the token in a file
posix_node() { # name, how to run a command there, fav path there, server url seen from there, token
	name=$1 pre=$2 bin=$3 u=$4 tok=$5
	printf '%s' "$tok" >"$root/token.$name"
	printf '{"node":{"allow_dirs":["%s/%s"]}}' "$r" "$name" >"$root/config.$name.json"
	$pre mkdir -p "$r/$name/home" "$r/$name/proj"
	$pre tee "$r/$name/token" <"$root/token.$name" >/dev/null
	$pre tee "$r/$name/home/config.json" <"$root/config.$name.json" >/dev/null
	bg $pre env $(envs "$name") "$bin" node --connect "$u" --token-file "$r/$name/token"
}
has() { for x in $hosts; do [ "$x" = "$1" ] && return 0; done; return 1; }
hosts="$*"
has mba && posix_node mba "ssh mba" /Users/ozn/.local/bin/fav "$url" "$tok_mba"
has linux && posix_node linux "ssh mba $docker exec -i fav-linux" /root/.local/bin/fav "ws://127.0.0.1:$port" "$tok_linux"
has wsl && posix_node wsl "ssh lg-win wsl -d Debian -e" /home/admin/.local/bin/fav "$url" "$tok_wsl"
w="$winhome\\$id\\win"
if has win; then
printf '@echo off\r\nset "TEND_HOME=%s\\home"\r\nset "CLAUDE_CONFIG_DIR=%s\\claude"\r\nset "CODEX_HOME=%s\\codex"\r\n"%s\\.local\\bin\\fav.exe" %%*\r\n' "$w" "$w" "$w" "$winhome" >"$root/fav.cmd"
printf '%s' "$tok_win" >"$root/token.win"
wj=$(printf '%s' "$w" | sed 's/\\/\\\\/g')
printf '{"node":{"allow_dirs":["%s"]}}' "$wj" >"$root/config.win.json"
ssh lg-win "mkdir $w\\home & mkdir $w\\proj" >/dev/null 2>&1
scp -q "$root/fav.cmd" "lg-win:$id/win/fav.cmd" && scp -q "$root/token.win" "lg-win:$id/win/token" && scp -q "$root/config.win.json" "lg-win:$id/win/home/config.json"
bg ssh -N -R "7789:$tailnet:$port" lg-win
bg ssh lg-win "$w\\fav.cmd node --connect ws://127.0.0.1:7789 --token-file $w\\token"
fi

printf '{"coordinator": {"url": "%s", "token_file": "%s/client-token"}}\n' "$url" "$root" >"$TEND_HOME/config.json"
printf '%s\n' "$tok_me" >"$root/client-token"

all_connected() {
	"$fav" machine list --json 2>/dev/null | python3 -c "import json,sys
try: print(sum(1 for m in json.load(sys.stdin) if m['state']=='connected'))
except ValueError: print(0)"
}
wait_connected() {
	deadline=$(($(date +%s) + $1))
	until [ "$(all_connected)" = "$nodes" ] || [ "$(date +%s)" -gt "$deadline" ]; do sleep 2; done
	"$fav" machine list
}
wait_connected 60
check "$nodes nodes connected" "$(all_connected)" "$nodes"

dir_of() { if [ "$1" = win ]; then printf '%s\\proj' "$w"; else printf '%s/%s/proj' "$r" "$1"; fi; }
jq_runs() { "$fav" run list --all --json | python3 -c "import json,sys; rs=json.load(sys.stdin); $1"; }
run_on() { # host, title
	"$fav" task add "$2" --machine "$1" --agent fake --dir "$(dir_of "$1")" >/dev/null
	t=$("$fav" task list --json | python3 -c "import json,sys; print([t['id'] for t in json.load(sys.stdin) if t['title']=='$2'][0])")
	"$fav" run start "$t" --runner background
}
wait_runs() {
	deadline=$(($(date +%s) + 120))
	while [ "$(jq_runs "print(sum(1 for r in rs if r['state'] in ('queued','starting','running','unknown')))")" != 0 ]; do
		[ "$(date +%s)" -gt "$deadline" ] && { echo "timed out"; break; }
		sleep 3
	done
}
for h in $hosts; do run_on "$h" "srv e2e on $h"; done
wait_runs
"$fav" run list --all
for h in $hosts; do
	check "$h exited 0" "$(jq_runs "print(' '.join(r['state']+str(r.get('exit_code')) for r in rs if r['machine']=='$h'))")" exited0
	run=$(jq_runs "print([r['id'] for r in rs if r['machine']=='$h'][0])")
	check "$h output" "$("$fav" run logs "$run" | grep -c 'fake step')" 2
done

stop_server
sleep 2
start_server
wait_connected 90
check "$nodes nodes back after the server restarts" "$(all_connected)" "$nodes"
check "the journal survived" "$(jq_runs "print(len(rs))")" "$nodes"
last=${hosts##* }
run_on "$last" "srv e2e after restart"
wait_runs
check "a run on $last after the restart" "$(jq_runs "print(sum(1 for r in rs if r['machine']=='$last' and r['state']=='exited'))")" 2

for p in $pids; do kill "$p" 2>/dev/null; done
stop_server
ckill "$id"
cexec rm -rf "$r"
ssh mba "pkill -f '$id'; rm -rf $r" || true
if has wsl; then
	ssh lg-win wsl -d Debian -e pkill -f "$id"
	ssh lg-win wsl -d Debian -e rm -rf "$r"
fi
if has win; then
	ssh lg-win "powershell -NoProfile -Command \"Get-CimInstance Win32_Process -Filter \\\"Name='fav.exe'\\\" | Where-Object { \$_.CommandLine -like '*$id*' } | ForEach-Object { Stop-Process -Id \$_.ProcessId -Force }\"" || true
	sleep 1
	ssh lg-win "rmdir /s /q $winhome\\$id" || true
fi
echo "$fail failed; client home $root, background log $root/bg.log"
[ "$fail" -eq 0 ]
