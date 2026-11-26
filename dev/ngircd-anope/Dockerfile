# ngircd with Anope services (Debian's packages), for the nick guard's
# end-to-end test (dev/e2e-managed.sh, TestManagedNickGuard): NickServ
# enforces registered nicks on a server without IRCv3.
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ngircd anope && rm -rf /var/lib/apt/lists/*
COPY ngircd.conf /etc/ngircd/ngircd.conf
COPY services.conf /etc/anope/dev-services.conf
# Test-friendly NickServ: no email, no wait between registrations.
RUN sed -i -e 's/^\(\s*\)forceemail = yes/\1forceemail = no/' -e 's/^\(\s*\)regdelay = 30s/\1regdelay = 1s/' /etc/anope/nickserv.conf \
 && grep -q 'forceemail = no' /etc/anope/nickserv.conf \
 && mkdir -p /run/anope /var/lib/anope/db && chown -R irc /run/anope /var/lib/anope /var/log/anope
EXPOSE 6667
CMD ["sh", "-c", "/usr/sbin/ngircd --config /etc/ngircd/ngircd.conf && sleep 1 && exec su -s /bin/sh irc -c 'exec /usr/sbin/anope --nofork --nopid --config=dev-services.conf --confdir=/etc/anope --dbdir=/var/lib/anope/db --logdir=/var/log/anope --modulesdir=/usr/lib/anope'"]
