# Container image for the system-level Broker end-to-end tests.
#
# It derives from the image built by test/e2e/compose/Dockerfile, which provides
# the release builds of peers and e2e-node and the peer and other accounts.
# This image adds systemd and the two groups the permission tests use:
#   peers-users (gid 2001) holds peer and restricts use of the system socket
#   peers-admins (gid 2002) starts empty; a test adds members at run time
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
        libnss-extrausers \
        acl \
        sudo \
        curl \
 && rm -rf /var/lib/apt/lists/*

RUN groupadd --gid 2001 peers-users \
 && groupadd --gid 2002 peers-admins \
 && usermod -aG peers-users peer

# diruser and dirgroup exist only in the extrausers source, not in /etc/passwd
# or /etc/group. A static Go binary that reads those files directly cannot see
# them; the account database tests require the Broker to resolve them through
# NSS. dirshare is a supplementary group of diruser, never its primary group,
# so only an initgroups lookup can grant access through it. The shadow entry
# has a locked password; it exists because starting diruser's user manager goes
# through PAM, which cannot set up the session without one.
RUN mkdir -p /var/lib/extrausers \
 && printf 'diruser:x:2100:2100:Directory Services User:/home/diruser:/bin/sh\n' \
        > /var/lib/extrausers/passwd \
 && printf 'dirgroup:x:2100:\ndirshare:x:2101:diruser\n' > /var/lib/extrausers/group \
 && printf 'diruser:*:19000:0:99999:7:::\n' > /var/lib/extrausers/shadow \
 && chgrp shadow /var/lib/extrausers/shadow \
 && chmod 0644 /var/lib/extrausers/passwd /var/lib/extrausers/group \
 && chmod 0640 /var/lib/extrausers/shadow \
 && mkdir -p /home/diruser \
 && chown 2100:2100 /home/diruser \
 && chmod 0755 /home/diruser \
 && sed -i -E \
        -e 's/^(passwd:[[:space:]]*).*/\1files extrausers/' \
        -e 's/^(group:[[:space:]]*).*/\1files extrausers/' \
        -e 's/^(shadow:[[:space:]]*).*/\1files extrausers/' \
        /etc/nsswitch.conf \
 && grep -Eq '^passwd:[[:space:]]+files[[:space:]]+extrausers' /etc/nsswitch.conf \
 && grep -Eq '^group:[[:space:]]+files[[:space:]]+extrausers' /etc/nsswitch.conf \
 && grep -Eq '^shadow:[[:space:]]+files[[:space:]]+extrausers' /etc/nsswitch.conf

STOPSIGNAL SIGRTMIN+3
ENTRYPOINT []
CMD ["/sbin/init"]
