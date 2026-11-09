# sandbox-agent POC. Build context: experiments/ (the parent), so the
# bridge and proxy are the sandbox-bridge rig's, unchanged.
FROM golang:1.26 AS build
WORKDIR /src
COPY sandbox-bridge/proxy/ proxy/
COPY sandbox-bridge/bridge/ bridge/
COPY sandbox-agent/gateway/ gateway/
RUN CGO_ENABLED=0 go build -C proxy -trimpath -o /out/egress-proxy . \
    && CGO_ENABLED=0 go build -C bridge -trimpath -o /out/stdio-bridge . \
    && CGO_ENABLED=0 go build -C gateway -trimpath -o /out/model-gateway .

FROM node:24-slim AS node

FROM ubuntu:26.04
# hadolint ignore=DL3008
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl git python3 \
    && rm -rf /var/lib/apt/lists/*
COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules/npm /usr/local/lib/node_modules/npm
RUN ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
    && npm install -g --no-fund --no-audit @anthropic-ai/claude-code@2.1.319 @google/gemini-cli@0.67.0 \
    && npm cache clean --force
COPY --from=build /out/ /usr/local/bin/
COPY sandbox-agent/allowlist.txt /etc/egress-proxy/allowlist.txt
COPY sandbox-agent/task/ /opt/task/
COPY sandbox-agent/launcher.sh sandbox-agent/agent.sh /opt/agent/
RUN chmod 0755 /opt/agent/*.sh && chmod -R a+rX /opt/task
ENTRYPOINT ["/opt/agent/launcher.sh"]
