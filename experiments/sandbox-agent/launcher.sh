#!/bin/sh
# Host container entrypoint for the sandbox-agent POC.
#
# The host is trusted: Cloud Run egress and the metadata server. It runs
# egress-proxy and model-gateway on Unix sockets, then runs agent.sh in the
# contexts the mode asks for:
#   sandbox               no egress; out only via stdio-bridge (under test)
#   sandbox-allow-egress  the same with --allow-egress (negative control)
#   host                  the host container itself, same bridge (control)
# Modes: attack (all three contexts, no agent), claude, gemini.
set -u
SANDBOX=/usr/local/gcp/bin/sandbox
MODE=${1:-attack}
log() { printf '{"time":"%s","component":"launcher","msg":"%s"}\n' "$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)" "$*"; }
log "start mode=$MODE execution=${CLOUD_RUN_EXECUTION:-none}"
mkdir -p /bridge && chmod 0777 /bridge

egress-proxy -unix /bridge/proxy.sock -allowlist /etc/egress-proxy/allowlist.txt &
case "$MODE" in
  claude) model-gateway -claude /bridge/claude.sock & ;;
  gemini) model-gateway -gemini /bridge/gemini.sock & ;;
  # attack: both backends, so the abuse probes have something to hit. The
  # probes only use refused paths, so no model call is made.
  attack) model-gateway -claude /bridge/claude.sock -gemini /bridge/gemini.sock & ;;
esac
for s in proxy gemini claude; do
  i=0
  while [ ! -S "/bridge/$s.sock" ] && [ "$i" -lt 100 ]; do
    sleep 0.05
    i=$((i + 1))
  done
done
log "sockets: $(ls /bridge | tr '\n' ' ')"

# Canary: a fake key on the host's disk, written before any sandbox starts.
# The probe must find exactly this file, which shows its scan can find one,
# and that a key on the host's disk would be readable from the sandbox. The
# marker is assembled at run time so this script never contains it.
umask 077
printf '%s-%s-%s\n' sk ant canary-not-a-real-key-000000 > /etc/agent-canary
umask 022

TARGETS=proxy=/bridge/proxy.sock,claude=/bridge/claude.sock,gemini=/bridge/gemini.sock
FWD=127.0.0.1:3128=proxy,127.0.0.1:8080=claude,127.0.0.1:8081=gemini
ENVS="--env PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

in_sandbox() { # "EXTRA_FLAGS" CTX AGENT_MODE
  # shellcheck disable=SC2086 # deliberate word splitting of flag lists
  stdio-bridge host -target "$TARGETS" -- \
    "$SANDBOX" "do" --stdin=true --write $1 $ENVS --env "CTX=$2" \
    -- stdio-bridge inside -forward "$FWD" -- /opt/agent/agent.sh "$3"
}
on_host() { # AGENT_MODE
  CTX=host stdio-bridge host -target "$TARGETS" -- \
    stdio-bridge inside -forward "$FWD" -- /opt/agent/agent.sh "$1"
}

case "$MODE" in
  attack)
    log "context sandbox"
    in_sandbox "" sandbox attack
    log "context sandbox-allow-egress"
    in_sandbox "--allow-egress" sandbox-allow-egress attack
    log "context host"
    on_host attack
    ;;
  claude | gemini)
    log "context sandbox, agent $MODE"
    in_sandbox "" sandbox "$MODE"
    ;;
  *)
    log "unknown mode"
    exit 2
    ;;
esac
log "done"
