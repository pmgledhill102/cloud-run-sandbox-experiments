# sandbox-bridge rig: egress-proxy and stdio-bridge as in the runner image,
# plus a Go client probe and the test scripts. No GitHub runner.
FROM golang:1.26 AS build
WORKDIR /src
COPY proxy/ proxy/
COPY bridge/ bridge/
COPY probe/ probe/
RUN CGO_ENABLED=0 go build -C proxy -trimpath -o /out/egress-proxy . \
    && CGO_ENABLED=0 go build -C bridge -trimpath -o /out/stdio-bridge . \
    && CGO_ENABLED=0 go build -C probe -trimpath -o /out/probe .

# A separate, minimal root for the --rootfs context: only what the tests need,
# and none of the host image's own paths.
FROM ubuntu:26.04 AS sbroot
# hadolint ignore=DL3008
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl openssl \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/ /usr/local/bin/
COPY inside.sh tests.sh /opt/rig/
RUN chmod 0755 /opt/rig/*.sh

FROM ubuntu:26.04
# hadolint ignore=DL3008
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl openssl python3 socat \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/ /usr/local/bin/
COPY allowlist.txt /etc/egress-proxy/allowlist.txt
COPY --from=sbroot / /opt/sbroot/
COPY launcher.sh inside.sh tests.sh channels.sh late.sh /opt/rig/
RUN chmod 0755 /opt/rig/*.sh
ENTRYPOINT ["/opt/rig/launcher.sh"]
