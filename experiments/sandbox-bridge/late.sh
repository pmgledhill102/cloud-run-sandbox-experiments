#!/bin/sh
# Runs in a sandbox: waits up to 10 s for a file the host writes after the
# sandbox has started. Seen = the host filesystem is a live view.
i=0
while [ "$i" -lt 20 ]; do
  if [ -e /etc/late-canary ]; then
    printf '{"component":"tests","ctx":"sandbox","check":"late-host-file","outcome":"ok","detail":"seen after %s half-seconds"}\n' "$i"
    exit 0
  fi
  sleep 0.5
  i=$((i + 1))
done
printf '{"component":"tests","ctx":"sandbox","check":"late-host-file","outcome":"fail","detail":"not seen within 10 s"}\n'
