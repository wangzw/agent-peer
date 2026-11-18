# Container image for the system-level Broker end-to-end tests.
#
# It derives from the image built by test/e2e/compose/Dockerfile, which provides
# the release builds of agent-peer and e2e-node and the peer and other accounts.
# This image adds systemd and the two groups the permission tests use:
#   agentpeer-users (gid 2001) holds peer and restricts use of the system socket
#   agentpeer-admins (gid 2002) starts empty; a test adds members at run time
ARG BASE
FROM ${BASE}

USER root

ENV LANG=C.UTF-8
ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        systemd \
        systemd-sysv \
        dbus \
        dbus-user-session \
        libpam-systemd \
        acl \
 && rm -rf /var/lib/apt/lists/*

RUN groupadd --gid 2001 agentpeer-users \
 && groupadd --gid 2002 agentpeer-admins \
 && usermod -aG agentpeer-users peer

STOPSIGNAL SIGRTMIN+3
ENTRYPOINT []
CMD ["/sbin/init"]
