#!/bin/sh
# The egress test matrix. Runs once per context (sandbox, sandbox with
# --allow-egress, host) with the bridge's proxy on 127.0.0.1:3128. Prints one
# JSON line per check with the raw outcome. The expected outcome depends on the
# context and is in README.md; this script does not judge.
set -u

CTX=${CTX:-unknown}
P=http://127.0.0.1:3128
MD='Metadata-Flavor: Google'
MDPATH=/computeMetadata/v1/project/project-id

esc() { tr -d '"\134' | tr '\n' ' ' | head -c "${1:-200}"; }

result() {
  printf '{"time":"%s","component":"tests","ctx":"%s","check":"%s","outcome":"%s","detail":"%s"}\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)" "$CTX" "$1" "$2" "$(printf '%s' "$3" | esc 300)"
}

# curl_check NAME CURL_ARGS... : ok when curl exits 0 with an HTTP status below 400.
curl_check() {
  name=$1
  shift
  out=$(curl -sS -o /dev/null -m 10 -w '%{http_code} %{http_connect}' "$@" 2> /tmp/curl.err)
  rc=$?
  code=${out%% *}
  conn=${out##* }
  err=$(esc 160 < /tmp/curl.err)
  if [ "$rc" -eq 0 ] && [ "${code:-0}" -lt 400 ] && [ "${code:-0}" -gt 0 ]; then
    result "$name" ok "rc=$rc http=$code connect=$conn"
  else
    result "$name" fail "rc=$rc http=$code connect=$conn $err"
  fi
}

# probe_check NAME PROBE_ARGS... : the Go client's own verdict.
probe_check() {
  name=$1
  shift
  out=$(timeout 15 probe "$@" 2>&1)
  case "$out" in
    ok*) result "$name" ok "$out" ;;
    *) result "$name" fail "$out" ;;
  esac
}

result identity info "uid=$(id -u) kernel=$(uname -r) nproc=$(nproc)"
result shared-loopback info "${SHARED_LOOPBACK:-unknown}"

# Host filesystem and environment canaries (see launcher.sh). In the host
# context these are expected to read; the question is the sandbox.
for f in /tmp/host-canary /etc/host-canary /root/host-canary /var/tmp/host-canary \
  /opt/rig/host-canary /home/host-canary /etc/egress-proxy/allowlist.txt; do
  if c=$(head -c 40 "$f" 2>&1); then
    result "host-file-readable:$f" ok "$(printf '%s' "$c" | head -1)"
  else
    result "host-file-readable:$f" fail "$c"
  fi
done
result host-env-canary info "RIG_HOST_ENV_CANARY=${RIG_HOST_ENV_CANARY:-unset}"
result root-mount info "$(grep -E '^[^ ]+ / ' /proc/mounts | head -2)"

# --- the spike's S2 matrix, repeated ---
curl_check allowlisted-via-proxy -x "$P" https://api.github.com/zen
curl_check other-host-via-proxy -x "$P" https://example.com/
curl_check direct-ignoring-proxy --noproxy '*' https://api.github.com/zen
curl_check direct-other-host --noproxy '*' https://example.com/
curl_check metadata-direct-ip --noproxy '*' -H "$MD" "http://169.254.169.254$MDPATH"
curl_check metadata-direct-name --noproxy '*' -H "$MD" "http://metadata.google.internal$MDPATH"
curl_check metadata-via-proxy-ip -x "$P" -H "$MD" "http://169.254.169.254$MDPATH"
curl_check metadata-via-proxy-name -x "$P" -H "$MD" "http://metadata.google.internal$MDPATH"
if getent hosts example.com > /dev/null 2>&1; then
  result dns-direct ok "getent resolved example.com"
else
  result dns-direct fail "getent could not resolve example.com"
fi

# --- extensions ---
# Direct to a public IP, no DNS needed at all (1.1.1.1 serves a cert for its IP).
curl_check direct-ipv4-literal --noproxy '*' https://1.1.1.1/
curl_check direct-ipv6-literal --noproxy '*' 'https://[2606:4700:4700::1111]/'
# IP literals through the proxy are refused by policy.
curl_check ipv4-literal-via-proxy -x "$P" https://1.1.1.1/
# A port other than 80/443 through the proxy.
curl_check port-22-via-proxy -x "$P" --connect-timeout 5 telnet://api.github.com:22
# Allowlisted names that resolve to private or metadata addresses: the
# DNS-rebinding shape. localtest.me -> 127.0.0.1; 169.254.169.254.nip.io -> metadata.
curl_check allowlisted-name-to-loopback -x "$P" http://localtest.me/
curl_check allowlisted-name-to-metadata -x "$P" -H "$MD" "http://169.254.169.254.nip.io$MDPATH"
# Mixed and private-only answers for an allowlisted name (see launcher.sh).
# Only the CONNECT status matters: 200 means the proxy dialled something.
for n in mixed.rig.test private.rig.test; do
  c=$(curl -sS -o /dev/null -m 10 -k -x "$P" -w '%{http_connect}' "https://$n/" 2> /dev/null)
  result "connect-$n" info "connect=$c"
done
# The Go client: honours HTTPS_PROXY by itself, and can ignore it.
probe_check go-proxy-allowlisted proxy-get https://api.github.com/zen
probe_check go-proxy-other proxy-get https://example.com/
probe_check go-direct-get direct-get https://api.github.com/zen
probe_check go-dial-ipv4 dial 1.1.1.1:443
probe_check go-dial-metadata dial 169.254.169.254:80
probe_check go-resolve resolve example.com

# Domain fronting: CONNECT names an allowlisted host, TLS then names another.
# The proxy sees only the CONNECT host. "connect=200" with a TLS handshake
# that carried SNI example.com shows the two are not bound together.
fr=$(printf '' | timeout 15 openssl s_client -proxy 127.0.0.1:3128 -connect api.github.com:443 \
  -servername example.com -brief 2>&1 | esc 300)
result fronting-sni-mismatch info "$fr"

# Throughput through the bridge: 50 MB from an allowlisted host.
t=$(curl -sS -o /dev/null -m 120 -x "$P" -w '%{http_code} %{size_download} %{time_total} %{speed_download}' \
  'https://speed.cloudflare.com/__down?bytes=50000000' 2>&1 | esc)
result throughput-50mb info "code bytes seconds bytes_per_s: $t"
