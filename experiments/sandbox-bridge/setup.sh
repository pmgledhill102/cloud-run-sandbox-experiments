#!/bin/sh
# Idempotent project setup for the sandbox-bridge rig. Safe to re-run.
#   PROJECT=<id> ./setup.sh
# Creates: an Artifact Registry repo, a build identity, and a job identity with
# no roles at all (the job only needs its stdout shipped, which Cloud Run does).
set -eu
: "${PROJECT:?set PROJECT}"
REGION=${REGION:-europe-west1}
G="gcloud --project $PROJECT --quiet"

$G services enable run.googleapis.com artifactregistry.googleapis.com cloudbuild.googleapis.com

$G artifacts repositories describe rig --location "$REGION" > /dev/null 2>&1 \
  || $G artifacts repositories create rig --location "$REGION" --repository-format docker \
    --description "sandbox-bridge rig images"

for sa in rig-build rig-job; do
  $G iam service-accounts describe "$sa@$PROJECT.iam.gserviceaccount.com" > /dev/null 2>&1 \
    || $G iam service-accounts create "$sa" --display-name "sandbox-bridge $sa"
done

B="serviceAccount:rig-build@$PROJECT.iam.gserviceaccount.com"
for role in roles/logging.logWriter roles/storage.objectViewer; do
  $G projects add-iam-policy-binding "$PROJECT" --member "$B" --role "$role" --condition None > /dev/null
done
$G artifacts repositories add-iam-policy-binding rig --location "$REGION" \
  --member "$B" --role roles/artifactregistry.writer > /dev/null
echo "setup done for $PROJECT"
