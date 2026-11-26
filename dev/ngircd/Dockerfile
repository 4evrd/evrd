# ngircd, a server without IRCv3, for managed mode's end-to-end test
# (dev/e2e-managed.sh).
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ngircd && rm -rf /var/lib/apt/lists/*
COPY ngircd.conf /etc/ngircd/ngircd.conf
EXPOSE 6667
CMD ["/usr/sbin/ngircd", "--nodaemon", "--config", "/etc/ngircd/ngircd.conf"]
