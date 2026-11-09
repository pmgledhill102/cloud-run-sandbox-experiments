#!/bin/sh
# Runs in each test context after stdio-bridge has put the proxy on
# 127.0.0.1:3128. Checks host-loopback sharing first, then runs the matrix.
set -u
if curl -s -m 2 --noproxy '*' -o /dev/null http://127.0.0.1:9999/; then
  SHARED_LOOPBACK=yes
else
  SHARED_LOOPBACK=no
fi
export SHARED_LOOPBACK
# tests.sh passes -x or --noproxy explicitly; the Go probe reads these.
export HTTPS_PROXY=http://127.0.0.1:3128 HTTP_PROXY=http://127.0.0.1:3128
export https_proxy=$HTTPS_PROXY http_proxy=$HTTP_PROXY NO_PROXY='' no_proxy=''
exec /opt/rig/tests.sh
