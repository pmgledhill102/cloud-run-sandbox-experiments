#!/bin/sh
# Host container entrypoint for the sandbox-bridge rig. No GitHub anywhere.
#
# The host container is trusted: it has Cloud Run's default egress and the
# metadata server. It runs egress-proxy on a Unix socket, then runs tests.sh in
# three contexts:
#   sandbox               Cloud Run sandbox, no egress (the configuration under test)
#   sandbox-allow-egress  the same with --allow-egress (negative control)
#   host                  the host container itself, through the same bridge (control)
# and then the boundary probes: which channels cross from host to sandbox.
#
# Modes: all (default), matrix, channels, versions
set -u

SANDBOX=/usr/local/gcp/bin/sandbox
MODE=${1:-all}
log() {
  printf '{"time":"%s","component":"launcher","msg":"%s"}\n' "$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)" "$*"
}

log "start mode=$MODE execution=${CLOUD_RUN_EXECUTION:-none}"
mkdir -p /bridge /work
chmod 0777 /bridge
chmod 1777 /work

egress-proxy -unix /bridge/proxy.sock -allowlist /etc/egress-proxy/allowlist.txt &
i=0
while [ ! -S /bridge/proxy.sock ] && [ "$i" -lt 100 ]; do
  sleep 0.05
  i=$((i + 1))
done
[ -S /bridge/proxy.sock ] || {
  log "proxy socket never appeared"
  exit 1
}
# Host filesystem canaries, written at run time (not in the image). If the
# sandbox can read them, any secret the host writes to disk is readable too.
umask 077
for d in /tmp /etc /root /var/tmp /opt/rig /home; do
  mkdir -p "$d" && printf 'rig-canary%s\n' "$d" > "$d/host-canary"
done
umask 022
# Deterministic DNS answers for the proxy's resolution check: one name with a
# mixed public and private answer, one private only. The proxy uses Go's
# resolver, which reads /etc/hosts first.
if printf '10.0.0.5 mixed.rig.test\n1.1.1.1 mixed.rig.test\n10.0.0.5 private.rig.test\n' >> /etc/hosts; then
  log "hosts entries added"
else
  log "could not write /etc/hosts"
fi
export RIG_HOST_ENV_CANARY=rig-canary-env
# A host-loopback listener: if the sandbox can reach it, it shares the host's network.
python3 -m http.server 9999 --bind 127.0.0.1 > /dev/null 2>&1 &

ENVS="--env PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

# in_sandbox "EXTRA_FLAGS" CTX : tests.sh inside a sandbox, out through the bridge.
in_sandbox() {
  # shellcheck disable=SC2086 # deliberate word splitting of flag lists
  stdio-bridge host -target proxy=/bridge/proxy.sock -- \
    "$SANDBOX" "do" --stdin=true --write $1 --mount type=bind,source=/work,destination=/work $ENVS --env "CTX=$2" \
    -- stdio-bridge inside -forward 127.0.0.1:3128=proxy -- /opt/rig/inside.sh
}

on_host() {
  CTX=host stdio-bridge host -target proxy=/bridge/proxy.sock -- \
    stdio-bridge inside -forward 127.0.0.1:3128=proxy -- /opt/rig/inside.sh
}

versions() {
  # The sandbox CLI documents no version command; its checksum identifies it.
  log "versions: sandbox-cli-sha256=$(sha256sum "$SANDBOX" | cut -c1-16) size=$(wc -c < "$SANDBOX")"
  log "versions: host-kernel=$(uname -r) image-os=$(. /etc/os-release && echo "$PRETTY_NAME")"
  log "versions: curl=$(curl --version | head -1 | tr -d '"\134') openssl=$(openssl version)"
}

matrix() {
  log "context sandbox"
  in_sandbox "" sandbox || log "sandbox context exited $?"
  log "context sandbox-allow-egress"
  in_sandbox "--allow-egress" sandbox-allow-egress || log "allow-egress context exited $?"
  log "context sandbox-rootfs"
  in_sandbox "--rootfs /opt/sbroot" sandbox-rootfs || log "rootfs context exited $?"
  log "context host"
  on_host || log "host context exited $?"
}

# Is the host filesystem a live view or a snapshot? The sandbox starts, then
# the host writes a file; the sandbox polls for it.
late() {
  "$SANDBOX" "do" --stdin=false $ENVS -- /opt/rig/late.sh &
  child=$!
  sleep 3
  printf 'rig-late-canary\n' > /etc/late-canary
  log "late canary written"
  wait "$child" || true
}

# Which --rootfs forms does the sandbox launcher accept? One line per form.
# "--rootfs /" is the control: it is the default, so it must work.
rootfs_probe() {
  mkdir -p /tmp/sbroot-copy && cp -a /opt/sbroot/. /tmp/sbroot-copy/ 2> /dev/null
  for form in "--rootfs /" "--rootfs /opt/sbroot" "--rootfs /opt/sbroot/" \
    "--rootfs /opt/sbroot --write" "--rootfs /opt/sbroot -w /" "--rootfs /tmp/sbroot-copy"; do
    # shellcheck disable=SC2086 # $form is a deliberate flag list
    out=$("$SANDBOX" "do" --stdin=false $form -- /bin/sh -c 'echo ok; head -1 /etc/os-release' 2>&1)
    rc=$?
    log "rootfs-probe [$form] rc=$rc out=$(printf '%s' "$out" | tr '\n' ' ' | tr -d '"\134' | cut -c1-200)"
  done
  log "rootfs-probe: sbroot top=$(ls /opt/sbroot | tr '\n' ' ')"
}

# A regular file on a bind mount, in each direction.
file_channel() {
  printf 'h2s-file\n' > /bridge/h2s.txt
  "$SANDBOX" "do" --stdin=false $ENVS --mount type=bind,source=/bridge,destination=/bridge \
    -- /bin/sh -c 'printf "{\"component\":\"channels\",\"probe\":\"file-h2s-read\",\"result\":\"%s\"}\n" "$(cat /bridge/h2s.txt 2>&1)"; printf s2h-file > /bridge/s2h.txt'
  log "file-s2h-read: $(cat /bridge/s2h.txt 2>&1 | tr -d '"\134')"
}

case "$MODE" in
  all)
    versions
    matrix
    late
    file_channel
    /opt/rig/channels.sh host
    ;;
  late) late ;;
  rootfs-probe) rootfs_probe ;;
  matrix) matrix ;;
  channels) /opt/rig/channels.sh host ;;
  versions) versions ;;
  *)
    log "unknown mode"
    exit 2
    ;;
esac
log "done"
