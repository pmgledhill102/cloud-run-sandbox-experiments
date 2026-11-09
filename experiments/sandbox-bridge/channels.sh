#!/bin/sh
# Which channels cross the host/sandbox boundary? Adapted from the spike's
# bridgeprobe.sh. Host side and sandbox side are the
# same script, selected by $1. Run by launcher.sh.
set -u
SANDBOX=/usr/local/gcp/bin/sandbox
say() { printf '{"time":"%s","component":"channels","side":"%s","probe":"%s","result":"%s"}\n' \
  "$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)" "$SIDE" "$1" "$(printf '%s' "$2" | tr -d '"\134' | tr '\n' ' ' | head -c 400)"; }

case "${1:-host}" in
  inside)
    SIDE=sandbox
    say mounts "$(grep -E ' /bridge | /work ' /proc/mounts)"
    say stat-socket "$(stat -c '%F %a %U' /bridge/proxy.sock 2>&1)"
    say connect-host-socket "$(printf 'GET / HTTP/1.0\r\n\r\n' | timeout 5 socat -d -d - UNIX-CONNECT:/bridge/proxy.sock 2>&1 | tail -3)"
    say python-connect "$(timeout 5 python3 -c 'import socket;s=socket.socket(socket.AF_UNIX);s.connect("/bridge/proxy.sock");print("connected")' 2>&1 | tail -1)"
    # FIFO written by the host, read here; and one written here, read by the host.
    say fifo-read "$(timeout 5 head -c 5 /bridge/h2s.fifo 2>&1)"
    say fifo-write "$( (printf 's2h!!' > /bridge/s2h.fifo) 2>&1 && echo written)"
    # Reverse socket: listen here, host connects.
    timeout 15 python3 -c '
import socket,os
p="/bridge/rev.sock"
s=socket.socket(socket.AF_UNIX); s.bind(p); os.chmod(p,0o777); s.listen(1); s.settimeout(12)
c,_=s.accept(); c.sendall(b"rev-ok"); c.close()' > /work/rev.out 2>&1 &
    sleep 8
    say reverse-listener "$(cat /work/rev.out 2> /dev/null) done"
    ;;
  stdio)
    # Echo stdin back to stdout byte for byte.
    exec cat
    ;;
  host)
    SIDE=host
    mkfifo /bridge/h2s.fifo /bridge/s2h.fifo
    chmod 666 /bridge/h2s.fifo /bridge/s2h.fifo
    (
      timeout 20 sh -c 'printf "h2s!!" > /bridge/h2s.fifo' 2>&1
      echo "h2s writer rc=$?"
    ) > /work/h2s.out &
    (
      timeout 20 head -c 5 /bridge/s2h.fifo 2>&1
      echo " s2h reader rc=$?"
    ) > /work/s2h.out &
    (
      sleep 6
      timeout 8 python3 -c '
import socket,time
for i in range(10):
    try:
        s=socket.socket(socket.AF_UNIX); s.connect("/bridge/rev.sock"); print("host got", s.recv(16)); break
    except Exception as e:
        err=e; time.sleep(0.5)
else: print("host connect failed", err)' > /work/rev-host.out 2>&1
    ) &
    "$SANDBOX" "do" --write \
      --mount type=bind,source=/bridge,destination=/bridge \
      --mount type=bind,source=/work,destination=/work \
      --env PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
      -- /opt/rig/channels.sh inside
    sleep 3
    say fifo-h2s "$(cat /work/h2s.out)"
    say fifo-s2h "$(cat /work/s2h.out)"
    say reverse-host-connect "$(cat /work/rev-host.out)"
    # stdio: is stdin passed through, and is the stream binary-clean?
    head -c 1048576 /dev/urandom > /work/rand.bin
    want=$(sha256sum < /work/rand.bin | cut -c1-16)
    got=$("$SANDBOX" "do" --env PATH=/usr/bin:/bin -- /opt/rig/channels.sh stdio < /work/rand.bin 2> /work/stdio.err | sha256sum | cut -c1-16)
    say stdio-binary "want=$want got=$got stderr=$(head -c 200 /work/stdio.err)"
    # stdio interactivity: does a line come back before stdin closes?
    # shellcheck disable=SC2016 # expanded inside the sandbox, not here
    got2=$( (
      printf 'ping\n'
      sleep 4
      printf 'late\n'
    ) | timeout 10 "$SANDBOX" "do" --env PATH=/usr/bin:/bin -- sh -c 'read l; echo "got:$l at $(date +%s)"; read l2; echo "got:$l2 at $(date +%s)"' 2>&1)
    say stdio-interactive "$got2 host_now=$(date +%s)"
    ;;
esac
