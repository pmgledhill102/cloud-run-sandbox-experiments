#!/bin/sh
# Create or update the POC job and start one execution.
#   PROJECT=<id> IMAGE=<ref> ./run.sh attack|claude|gemini
set -eu
: "${PROJECT:?set PROJECT}" "${IMAGE:?set IMAGE}"
export PROJECT IMAGE
cd "$(dirname "$0")"
sed -e "s|\${PROJECT}|$PROJECT|g" -e "s|\${IMAGE}|$IMAGE|g" job.yaml.tmpl > /tmp/sandbox-agent-job.yaml
python3 crun.py apply /tmp/sandbox-agent-job.yaml
python3 crun.py run sandbox-agent-poc "${1:-attack}"
