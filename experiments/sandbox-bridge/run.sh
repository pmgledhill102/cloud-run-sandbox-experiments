#!/bin/sh
# Create or update the rig job and start one execution.
#   PROJECT=<id> IMAGE=<ref> ./run.sh [MODE]
set -eu
: "${PROJECT:?set PROJECT}" "${IMAGE:?set IMAGE}"
export PROJECT IMAGE
cd "$(dirname "$0")"
sed -e "s|\${PROJECT}|$PROJECT|g" -e "s|\${IMAGE}|$IMAGE|g" job.yaml.tmpl > /tmp/rig-job.yaml
python3 crun.py apply /tmp/rig-job.yaml
python3 crun.py run sandbox-bridge-rig "${1:-all}"
