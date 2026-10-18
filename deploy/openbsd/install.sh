#!/bin/sh
#
# Installs (or upgrades) evrd on OpenBSD. Run as root:
#
#	ftp -o - https://raw.githubusercontent.com/4evrd/evrd/main/deploy/openbsd/install.sh | sh
#
# (or download it and run sh install.sh; pass options with | sh -s -- -d host)
#
# From a terminal it asks where evrd will live (host names, ports, the
# certificate, who serves HTTPS, the first admin); options answer ahead:
#	-d host		the web app's host name
#	-c cert		letsencrypt (automatic, renewed), selfsigned, or none
#			(plain HTTP, for trying evrd out)
#	-w server	who serves HTTPS: evrd (built in) or relayd
#	-s port		evrd's HTTPS port (default 443)
#	-p port		evrd's plain-HTTP port: the web app with -c none, else
#			local only, behind relayd (default 8098)
#	-e email	a contact address for Let's Encrypt (optional)
#	-o issuer	single sign-on: the OpenID Connect issuer URL (e.g.
#			https://id.example.org/oidc for Logto)
#	-O client-id	its client ID (the secret: $EVRD_OIDC_CLIENT_SECRET,
#			or asked)
#	-L label	the sign-in button: "Continue with <label>"
#	-R role		a provider role that makes evrd admins
#	-i host		the host name IRC clients use (default: the web host)
#	-t port		the IRC port, with TLS (default 6697)
#	-P port		a plain-text IRC port too (default none)
#	-a name		create an admin account (password asked, or from
#			$EVRD_ADMIN_PASSWORD)
#	-v version	install this release (default: the latest)
#	-f archive	install from a local evrd_openbsd_<arch>.tar.gz, with
#			its SHA256 file next to it
#	-r		write a new config (the old one is kept as .bak)
#	-y		don't ask: use the options and defaults
#	-n		install, but don't start evrd
#
# What goes where:
#	/usr/local/bin/evrd, evrctl	the programs
#	/usr/local/bin/evrd-links	holds the IRC connections while evrd
#					restarts, so upgrades don't drop anyone
#	/etc/evrd/evrd.conf		the config (kept on upgrades)
#	/etc/evrd/secret.key		encrypts saved network passwords and
#					certificate keys
#	/var/evrd			evrd's chroot: data/ (database and
#					history), web/ (the web app), etc/
#	/etc/rc.d/evrd			rcctl start|stop|restart evrd
#	/etc/rc.d/evrd_links		the connection holder, started first
#	/usr/local/share/examples/evrd	relayd, httpd, acme-client and pf
#
# evrd starts as root, binds its ports, reads its config, key and
# certificate, then chroots to /var/evrd, becomes _evrd, and unveils and
# pledges itself: from then on it sees only its own data and can't run
# anything.

# Everything is in main, called on the last line: piped into sh, the whole
# script is read before any of it runs.
main() {
set -eu

repo=4evrd/evrd
user=_evrd
jail=/var/evrd
etc=/etc/evrd
conf=$etc/evrd.conf
examples=/usr/local/share/examples/evrd

web_host= certs= front= https_port= web_port= email= irc_host= irc_port= plain_port= admin=
oidc_issuer= oidc_id= oidc_label= oidc_role=
version= archive= reconf=no batch=no start=yes
while getopts d:c:w:s:p:e:o:O:L:R:i:t:P:a:v:f:ryn opt; do
	case $opt in
	d) web_host=$OPTARG ;;
	c) certs=$OPTARG ;;
	w) front=$OPTARG ;;
	s) https_port=$OPTARG ;;
	p) web_port=$OPTARG ;;
	e) email=$OPTARG ;;
	o) oidc_issuer=$OPTARG ;;
	O) oidc_id=$OPTARG ;;
	L) oidc_label=$OPTARG ;;
	R) oidc_role=$OPTARG ;;
	i) irc_host=$OPTARG ;;
	t) irc_port=$OPTARG ;;
	P) plain_port=$OPTARG ;;
	a) admin=$OPTARG ;;
	v) version=${OPTARG#v} ;;
	f) archive=$OPTARG ;;
	r) reconf=yes ;;
	y) batch=yes ;;
	n) start=no ;;
	*) echo "options: https://github.com/4evrd/evrd/blob/main/docs/openbsd.md#without-questions" >&2; exit 2 ;;
	esac
done

say() { printf '==> %s\n' "$*"; }
die() { printf 'install.sh: %s\n' "$*" >&2; exit 1; }

[ "$(uname -s)" = OpenBSD ] || die "this is for OpenBSD"
[ "$(id -u)" = 0 ] || die "run as root"
arch=$(uname -m)
case $arch in
amd64|arm64) ;;
*) die "no evrd build for $arch (amd64 and arm64 only)" ;;
esac
name=evrd_openbsd_$arch
# Answers come from the terminal, even when the script itself comes down a
# pipe (ftp -o - … | sh); without one, the defaults.
if [ -t 0 ]; then
	exec 3<&0
elif (exec </dev/tty) 2>/dev/null; then
	exec 3</dev/tty
else
	batch=yes
fi

# ask var "question" default [check]: asks until check (a function) accepts
# the answer; without a terminal (or with -y) takes the default.
ask() {
	_var=$1 _q=$2 _def=$3 _check=${4:-true}
	while :; do
		if [ "$batch" = yes ]; then
			_ans=$_def
		else
			if [ -n "$_def" ]; then printf '%s [%s] ' "$_q" "$_def"; else printf '%s ' "$_q"; fi
			read -r _ans <&3 || _ans=
			_ans=${_ans:-$_def}
		fi
		if $_check "$_ans"; then
			eval "$_var=\$_ans"
			return
		fi
		[ "$batch" = yes ] && die "$_q: \"$_ans\" won't do"
	done
}
is_host() {
	case $1 in
	''|*[!A-Za-z0-9.-]*|.*|-*) echo "   a host name, like bnc.example.org"; return 1 ;;
	esac
}
is_port() {
	case $1 in
	''|*[!0-9]*) echo "   a port number"; return 1 ;;
	esac
	[ "$1" -ge 1 ] && [ "$1" -le 65535 ] || { echo "   1-65535"; return 1; }
}
is_port_or_none() { [ "$1" = none ] || is_port "$1"; }
is_yn() {
	case $1 in
	y|yes|Y|Yes|YES|n|no|N|No|NO) ;;
	*) echo "   yes or no"; return 1 ;;
	esac
}
yes() { case $1 in y*|Y*) return 0 ;; esac; return 1; }
is_certs() {
	case $1 in
	letsencrypt|selfsigned|none) ;;
	*) echo "   letsencrypt, selfsigned or none"; return 1 ;;
	esac
}
is_front() {
	case $1 in
	evrd|relayd) ;;
	*) echo "   evrd or relayd"; return 1 ;;
	esac
}
is_email() {
	case $1 in
	'') ;;
	*@*.*) ;;
	*) echo "   an email address, or Enter for none"; return 1 ;;
	esac
}
is_url_or_empty() {
	case $1 in
	''|https://?*) ;;
	*) echo "   an https:// URL, or Enter to skip"; return 1 ;;
	esac
}
nonempty() { [ -n "$1" ] || { echo "   required"; return 1; }; }
# secret var "question": asks without echoing.
secret() {
	while :; do
		stty -echo <&3
		printf '%s ' "$2"; read -r _s <&3 || _s=; echo
		stty echo <&3
		[ -n "$_s" ] && break
		echo "   required"
	done
	eval "$1=\$_s"
	_s=
}
is_user() {
	case $1 in
	''|*[!A-Za-z0-9._-]*) echo "   letters, digits, . _ -"; return 1 ;;
	esac
}
# listening reports whether something on this machine listens on TCP port $1.
listening() { netstat -an -p tcp | awk '$NF == "LISTEN" { print $4 }' | grep -q "\.$1\$"; }

tmp=$(mktemp -d /tmp/evrd-install.XXXXXXXXXX)
trap '{ stty echo <&3; } 2>/dev/null || :; rm -rf "$tmp"' EXIT INT TERM

# --- questions -------------------------------------------------------------
writeconf=yes
if [ -f "$conf" ] && [ "$reconf" = no ]; then
	writeconf=no
	if [ "$batch" = no ]; then
		ask keep "Keep the current $conf?" yes is_yn
		yes "$keep" || writeconf=yes
	fi
fi

if [ "$writeconf" = yes ]; then
	[ "$batch" = no ] && echo "
Where will evrd live? (Enter takes the [default].)"
	ask web_host "Host name for the web app:" "${web_host:-$(hostname)}" is_host
	if [ -z "$certs" ]; then
		case $web_host in
		*.my.domain|*.local|*.lan|*.home|*.internal|*.test) certs=selfsigned ;;
		*.*) certs=letsencrypt ;;
		*) certs=selfsigned ;;
		esac
	fi
	[ "$batch" = no ] && echo "
The HTTPS certificate (the IRC TLS port uses it too):
  letsencrypt  a real one, got and renewed automatically — $web_host's
               DNS must point here, with ports 80 and 443 open
  selfsigned   made here; browsers and IRC clients will warn about it
  none         plain HTTP on a port of its own, for trying evrd out"
	ask certs "Certificate:" "$certs" is_certs

	if [ "$certs" != none ]; then
		[ "$batch" = no ] && echo "
Who serves HTTPS:
  evrd    evrd itself, on 443 (and 80, to send browsers there and answer
          Let's Encrypt) — nothing else to run; change it later in the web
          app under Admin → Settings → HTTPS & TLS
  relayd  OpenBSD's relayd in front, with httpd and acme-client for Let's
          Encrypt — if this machine serves other sites on 443 too"
		if [ -z "$front" ]; then
			# relayd if something else has 443 (evrd's own doesn't count)
			front=evrd
			if ! rcctl check evrd >/dev/null 2>&1 && listening 443; then
				front=relayd
			fi
		fi
		ask front "Who serves HTTPS?" "$front" is_front
		if [ "$front" = evrd ]; then
			ask https_port "HTTPS port:" "${https_port:-443}" is_port
			web_port=${web_port:-8098}
		else
			https_port=443
			ask web_port "Port evrd listens on behind relayd (local only):" "${web_port:-8098}" is_port
		fi
		[ "$certs" = letsencrypt ] && ask email "Email for Let's Encrypt's expiry notices (optional):" "$email" is_email
	else
		front=none
		ask web_port "Port for the web app:" "${web_port:-8098}" is_port
	fi
	ask irc_host "Host name IRC clients connect to:" "${irc_host:-$web_host}" is_host
	ask irc_port "IRC port (TLS):" "${irc_port:-6697}" is_port
	ask plain_port "Plain-text IRC port too (unencrypted; \"none\" for none):" "${plain_port:-none}" is_port_or_none

	# evrd binds its ports at startup: they must be free (or its own).
	if [ "$front" = evrd ] && ! rcctl check evrd >/dev/null 2>&1; then
		for p in "$https_port" 80; do
			listening "$p" && die "port $p is in use (httpd? relayd?) — free it, or rerun and choose relayd"
		done
	fi

	case $front in
	none) public=http://$web_host:$web_port ;;
	*)
		public=https://$web_host
		[ "$https_port" = 443 ] || public=$public:$https_port
		;;
	esac
	[ "$batch" = no ] && echo "
Single sign-on (optional): sign in through an OpenID Connect provider such
as Logto, Authentik or Keycloak. Register evrd there as a traditional web
app with the redirect URI
  $public/auth/callback"
	ask oidc_issuer "Issuer URL (Enter to skip):" "$oidc_issuer" is_url_or_empty
	oidc_secret=
	if [ -n "$oidc_issuer" ]; then
		ask oidc_id "Client ID:" "$oidc_id" nonempty
		oidc_secret=${EVRD_OIDC_CLIENT_SECRET:-}
		if [ -z "$oidc_secret" ]; then
			[ "$batch" = no ] || die "-o: set EVRD_OIDC_CLIENT_SECRET, or run from a terminal"
			secret oidc_secret "Client secret:"
		fi
		ask oidc_label "Sign-in button — \"Continue with …\":" "${oidc_label:-single sign-on}" nonempty
		ask oidc_role "A provider role that makes evrd admins (Enter for none):" "$oidc_role" true
	fi
fi
if [ -z "$admin" ] && [ "$batch" = no ]; then
	ask admin "Create an admin account — username (Enter to skip):" "" true
	[ -z "$admin" ] || is_user "$admin" || die "bad username"
fi
pw=
if [ -n "$admin" ]; then
	pw=${EVRD_ADMIN_PASSWORD:-}
	if [ -z "$pw" ]; then
		[ "$batch" = no ] || die "-a: set EVRD_ADMIN_PASSWORD, or run from a terminal"
		while :; do
			stty -echo <&3
			printf 'Password for %s (10 characters or more): ' "$admin"; read -r pw <&3; echo
			printf 'Again: '; read -r pw2 <&3; echo
			stty echo <&3
			[ "$pw" = "$pw2" ] && [ ${#pw} -ge 10 ] && break
			echo "   they must match and be 10 characters or more"
		done
		pw2=
	fi
fi

# --- get and check the release ---------------------------------------------
if [ -n "$archive" ]; then
	[ -f "$archive" ] || die "$archive: no such file"
	sums=$(dirname "$archive")/SHA256
	[ -f "$sums" ] || die "$sums: missing (it comes with the release)"
	cp "$archive" "$tmp/$name.tar.gz"
	cp "$sums" "$tmp/SHA256"
else
	if [ -n "$version" ]; then
		url=https://github.com/$repo/releases/download/v$version
	else
		url=https://github.com/$repo/releases/latest/download
	fi
	say "downloading $name.tar.gz ${version:-(latest)}"
	ftp -V -o "$tmp/SHA256" "$url/SHA256" >/dev/null || die "couldn't fetch $url/SHA256"
	ftp -V -o "$tmp/$name.tar.gz" "$url/$name.tar.gz" >/dev/null || die "couldn't fetch $url/$name.tar.gz"
fi
(cd "$tmp" && sha256 -q -C SHA256 "$name.tar.gz") || die "$name.tar.gz doesn't match its SHA256 checksum"
tar -C "$tmp" -xzf "$tmp/$name.tar.gz"
src=$tmp/$name
[ -x "$src/evrd" ] && [ -f "$src/web/index.html" ] || die "$name.tar.gz isn't an evrd release"

# --- the user and the directories ------------------------------------------
if ! id "$user" >/dev/null 2>&1; then
	# a system uid below 1000 that neither passwd nor group uses
	uid=799
	while grep -q "^[^:]*:[^:]*:$uid:" /etc/master.passwd /etc/group; do
		uid=$((uid - 1))
	done
	say "adding user $user ($uid)"
	groupadd -g "$uid" "$user"
	useradd -u "$uid" -g "$user" -c "evrd IRC bouncer" -d /var/empty -s /sbin/nologin "$user"
fi

# The chroot and everything in it but data/ and tmp/ belong to root, so a
# compromised evrd can't change its own web app or name resolution.
install -d -o root -g wheel -m 755 "$jail" "$jail/etc"
install -d -o "$user" -g "$user" -m 700 "$jail/data" "$jail/tmp"
install -d -o root -g "$user" -m 750 "$jail/run"
install -d -o root -g "$user" -m 750 "$etc"

# --- the programs and the web app ------------------------------------------
running=no
rcctl check evrd >/dev/null 2>&1 && running=yes
# A release that only changes the web app needs no restart: evrd serves it
# from disk, so nobody's connections drop.
changed=yes
if [ -f /usr/local/bin/evrd ] && [ "$(sha256 -q /usr/local/bin/evrd)" = "$(sha256 -q "$src/evrd")" ]; then
	changed=no
fi
# The holder keeps everyone's IRC connections while evrd restarts. It
# rarely changes; when it does, restarting it drops the connections once.
holder=no links_changed=no links_running=no
if [ -f "$src/evrd-links" ]; then
	holder=yes
	rcctl check evrd_links >/dev/null 2>&1 && links_running=yes
	if [ ! -f /usr/local/bin/evrd-links ] || [ "$(sha256 -q /usr/local/bin/evrd-links)" != "$(sha256 -q "$src/evrd-links")" ]; then
		links_changed=yes
	fi
	install -o root -g bin -m 555 "$src/evrd-links" /usr/local/bin/
fi
install -o root -g bin -m 555 "$src/evrd" "$src/evrctl" /usr/local/bin/
rm -rf "$jail/web.new" "$jail/web.old"
cp -R "$src/web" "$jail/web.new"
chown -R root:wheel "$jail/web.new"
chmod -R u=rwX,go=rX "$jail/web.new"
[ -d "$jail/web" ] && mv "$jail/web" "$jail/web.old"
mv "$jail/web.new" "$jail/web"
rm -rf "$jail/web.old"
say "installed $(/usr/local/bin/evrd -version)"

# --- the key and the config ------------------------------------------------
if [ ! -f "$etc/secret.key" ]; then
	(umask 077 && /usr/local/bin/evrctl genkey >"$etc/secret.key")
	say "made $etc/secret.key — back it up apart from $jail"
fi
chown root:"$user" "$etc/secret.key"
chmod 640 "$etc/secret.key"

cert=/etc/ssl/$web_host.crt
key=/etc/ssl/private/$web_host.key
# evrd re-reads its certificate after a renewal (rcctl reload evrd) instead
# of restarting — from a copy inside its chroot, kept fresh by rc.d and cron.
jtls=$jail/etc/tls
jcert=$jtls/$web_host.crt
jkey=$jtls/$web_host.key
links_sock=$jail/run/links.sock
if [ "$writeconf" = yes ]; then
	tls=
	case $front in
	evrd)
		src_tls=acme
		[ "$certs" = selfsigned ] && src_tls=selfsigned
		web="# evrd serves HTTPS itself; http_listen sends browsers there and
# answers Let's Encrypt. The certificate settings can be changed in the web
# app (Admin → Settings → HTTPS & TLS).
public_url   $public
https_listen :$https_port
http_listen  :80
api          127.0.0.1:$web_port
tls          $src_tls"
		[ -n "$email" ] && web="$web
acme_email   $email"
		;;
	relayd)
		web="# Behind relayd for HTTPS ($examples/relayd.conf).
public_url $public
api        127.0.0.1:$web_port"
		# acme-client's (or the self-signed) certificate, for IRC
		tls="
tls_cert $jcert
tls_key  $jkey"
		;;
	none)
		web="# Plain HTTP, for trying evrd out: passwords cross the network
# unencrypted. For real use, rerun install.sh -r and choose a certificate.
public_url    http://$web_host:$web_port
api           :$web_port
cookie_secure no"
		;;
	esac
	sso=
	if [ -n "$oidc_issuer" ]; then
		sso="
# Single sign-on (also under Admin → Settings → Single sign-on). The
# provider redirects back to $public/auth/callback.
oidc_issuer        $oidc_issuer
oidc_client_id     $oidc_id
oidc_client_secret $oidc_secret
oidc_label         $oidc_label"
		[ -z "$oidc_role" ] || sso="$sso
oidc_admin_role    $oidc_role"
		sso="$sso
"
	fi
	links=
	[ "$holder" = no ] || links="
# IRC connections stay up while evrd restarts, held by evrd-links.
links_socket $links_sock
"
	plain=
	[ "$plain_port" = none ] || plain="
listen irc+insecure://:$plain_port   # unencrypted"
	if [ -f "$conf" ]; then
		cp -p "$conf" "$conf.bak"
		say "kept the old config as $conf.bak"
	fi
	(umask 027 && cat >"$conf" <<EOF
# evrd, written by install.sh. Every directive:
# https://github.com/$repo/blob/main/docs/configuration.md
# Most settings can also be changed in the web app, under Admin → Settings.

irc_host $irc_host

$web

$sso
# IRC clients connect with TLS, with the same certificate.
listen ircs://:$irc_port$plain$tls

# Everything on local disk, inside the chroot: no database server needed.
db       sqlite:$jail/data/evrd.db
history  pebble:$jail/data/history
web_root $jail/web

# Encrypts saved network passwords and certificate keys.
secret_key_file $etc/secret.key
$links
# evrd binds its ports and reads this file, the key and any certificate
# files as root, then chroots to $jail and runs as $user, unveiled and
# pledged.
chroot $jail
user   $user
EOF
	)
	say "wrote $conf"
else
	say "kept $conf"
	certs=kept
	# what the summary and the examples need
	web_host=$(sed -n 's|^public_url[[:space:]]*https*://\([^:/]*\).*|\1|p' "$conf")
	irc_host=$(sed -n 's/^irc_host[[:space:]]*//p' "$conf")
	web_port=$(sed -n 's/^api[[:space:]]*.*:\([0-9]*\).*/\1/p' "$conf")
	irc_port=$(sed -n 's|^listen[[:space:]]*ircs://.*:\([0-9]*\).*|\1|p' "$conf" | head -1)
	https_port=$(sed -n 's/^https_listen[[:space:]]*.*:\([0-9]*\).*/\1/p' "$conf")
	if [ -n "$https_port" ]; then
		front=evrd
	elif grep -q '^public_url[[:space:]]*https:' "$conf"; then
		front=relayd https_port=443
	else
		front=none
	fi
	web_host=${web_host:-$(hostname)} irc_host=${irc_host:-$web_host}
	web_port=${web_port:-8098} irc_port=${irc_port:-6697}
	cert=/etc/ssl/$web_host.crt key=/etc/ssl/private/$web_host.key
	jcert=$jtls/$web_host.crt jkey=$jtls/$web_host.key
	# configs from before evrd could reload its certificate: point them at
	# the copies inside the chroot
	if [ "$front" = relayd ] && grep -q "^tls_cert[[:space:]]*$cert\$" "$conf"; then
		cp -p "$conf" "$conf.bak.tls"
		sed -i -e "s|^tls_cert[[:space:]].*|tls_cert $jcert|" -e "s|^tls_key[[:space:]].*|tls_key  $jkey|" "$conf"
		say "tls_cert now points at the copy in the chroot (reloaded on renewal, no restart)"
		changed=yes
	fi
	if [ "$holder" = yes ] && ! grep -q '^links_socket' "$conf"; then
		printf '\n# IRC connections stay up while evrd restarts, held by evrd-links.\nlinks_socket %s\n' "$links_sock" >>"$conf"
		say "added links_socket: from now on, restarting evrd doesn't disconnect anyone"
		changed=yes
	fi
fi
chown root:"$user" "$conf"
chmod 640 "$conf"

# --- rc.d --------------------------------------------------------------------
rc_certs= copy_certs=
if [ "$front" = relayd ]; then
	install -d -o root -g "$user" -m 750 "$jtls"
	copy_certs="install -o root -g $user -m 640 $cert $key $jtls/"
	[ -f "$cert" ] && [ -f "$key" ] && $copy_certs
	rc_certs="	# and reads its certificate from a copy inside it
	[ -f $cert ] && [ -f $key ] && $copy_certs
"
fi
cat >/etc/rc.d/evrd <<EOF
#!/bin/ksh
#
# evrd, the IRC bouncer (installed by evrd's install.sh)

daemon="/usr/local/bin/evrd"
daemon_flags="-config $conf"
daemon_logger="daemon.info"

. /etc/rc.d/rc.subr

rc_bg=YES
# rcctl reload evrd: SIGHUP, evrd re-reads its certificate (no restart)

rc_pre() {
	# evrd looks up IRC servers from inside its chroot
	for f in resolv.conf hosts; do
		[ -f /etc/\$f ] && install -o root -g wheel -m 444 /etc/\$f $jail/etc/\$f
	done
$rc_certs	return 0
}

rc_cmd \$1
EOF
chmod 555 /etc/rc.d/evrd

if [ "$holder" = yes ]; then
	cat >/etc/rc.d/evrd_links <<EOF
#!/bin/ksh
#
# evrd-links, which holds evrd's IRC connections while evrd restarts
# (installed by evrd's install.sh). Restarting it drops them.

daemon="/usr/local/bin/evrd-links"
daemon_flags="-socket $links_sock -chroot $jail -user $user"
daemon_logger="daemon.info"

. /etc/rc.d/rc.subr

rc_bg=YES
rc_reload=NO

rc_cmd \$1
EOF
	chmod 555 /etc/rc.d/evrd_links
fi

# --- examples: relayd, httpd, acme-client, pf --------------------------------
alt=
[ "$irc_host" = "$web_host" ] || alt="
	alternative names { $irc_host }"
install -d -m 755 "$examples"
cat >"$examples/relayd.conf" <<EOF
# HTTPS for the web app with relayd in front of evrd: relayd terminates TLS
# and passes the API, its WebSocket and the app to evrd. Copy to
# /etc/relayd.conf, then rcctl enable relayd && rcctl start relayd.
# (Not needed when evrd serves HTTPS itself: https_listen in evrd.conf.)
table <evrd> { 127.0.0.1 }

http protocol "evrd" {
	tls keypair "$web_host"
	match request header set "X-Forwarded-For" value "\$REMOTE_ADDR"
	match request header set "X-Forwarded-Proto" value "https"
	http websockets
}

relay "evrd" {
	listen on 0.0.0.0 port 443 tls
	listen on :: port 443 tls
	protocol "evrd"
	session timeout 3600
	forward to <evrd> port $web_port
}
EOF
cat >"$examples/httpd.conf" <<EOF
# Port 80 with relayd: answers acme-client's challenges, sends everything
# else to HTTPS. Copy to /etc/httpd.conf, then rcctl enable httpd &&
# rcctl start httpd.
server "$web_host" {
	listen on * port 80
	location "/.well-known/acme-challenge/*" {
		root "/acme"
		request strip 2
	}
	location * {
		block return 301 "https://\$HTTP_HOST\$REQUEST_URI"
	}
}
EOF
cat >"$examples/acme-client.conf" <<EOF
# A Let's Encrypt certificate for relayd and evrd's IRC port. Copy to
# /etc/acme-client.conf, start httpd, then: acme-client -v $web_host
authority letsencrypt {
	api url "https://acme-v02.api.letsencrypt.org/directory"
	account key "/etc/acme/letsencrypt-privkey.pem"
}

domain $web_host {$alt
	domain key "$key"
	domain full chain certificate "$cert"
	sign with letsencrypt
}

# Renewal, in root's crontab (crontab -e):
# ~ 3 * * * acme-client $web_host && install -o root -g $user -m 640 $cert $key $jtls/ && rcctl reload relayd && rcctl reload evrd
EOF
ports=$irc_port
[ "$plain_port" = none ] || [ -z "$plain_port" ] || ports="$irc_port $plain_port"
case $front in
evrd) webports="80 $https_port" ;;
relayd) webports="80 443" ;;
*) webports=$web_port ;;
esac
cat >"$examples/pf.conf" <<EOF
# Add to /etc/pf.conf (after the default block rule), then pfctl -f /etc/pf.conf:
pass in on egress proto tcp to port { $webports }	# the web app
pass in on egress proto tcp to port { $ports }	# IRC clients
EOF

# --- relayd's certificate, if relayd serves HTTPS ------------------------------
# Installs a config only where none is (else says what to merge).
place() {
	if [ ! -f "/etc/$1" ]; then
		install -m 644 "$examples/$1" "/etc/$1"
		say "wrote /etc/$1"
	elif ! cmp -s "$examples/$1" "/etc/$1"; then
		say "/etc/$1 exists already: merge $examples/$1 into it"
		return 1
	fi
}
selfsigned() {
	san="DNS:$web_host"
	[ "$irc_host" = "$web_host" ] || san="$san,DNS:$irc_host"
	# RSA: relayd won't load an EC key made this way
	openssl req -x509 -newkey rsa:2048 -nodes \
		-days 3650 -subj "/CN=$web_host" -addext "subjectAltName=$san" \
		-keyout "$key" -out "$cert" 2>/dev/null || die "openssl couldn't make a certificate"
	chmod 600 "$key"
	chmod 644 "$cert"
	say "made a self-signed certificate for $web_host ($cert)"
}
if [ "$front" = relayd ] && [ "$certs" != kept ]; then
	if [ "$certs" = letsencrypt ]; then
		if place httpd.conf; then
			rcctl enable httpd >/dev/null
			rcctl restart httpd >/dev/null
		fi
		place acme-client.conf || true
		if acme-client -v "$web_host"; then
			say "got a Let's Encrypt certificate for $web_host"
		else
			say "Let's Encrypt said no — using a self-signed certificate for now"
			selfsigned
			certs=fallback
		fi
		if ! crontab -l 2>/dev/null | grep -q "acme-client $web_host"; then
			(crontab -l 2>/dev/null
			 echo "~ 3 * * * acme-client $web_host && $copy_certs && rcctl reload relayd && rcctl reload evrd") | crontab -
			say "added certificate renewal to root's crontab"
		fi
	elif [ -f "$cert" ] && [ -f "$key" ]; then
		say "using the certificate already in $cert"
	else
		selfsigned
	fi
	if place relayd.conf; then
		rcctl enable relayd >/dev/null
		rcctl restart relayd >/dev/null
	fi
fi

# Renewal from before evrd could reload its certificate: copy and reload,
# not restart.
if [ "$front" = relayd ] && crontab -l 2>/dev/null | grep -q "acme-client $web_host && rcctl reload relayd && rcctl restart evrd"; then
	crontab -l | sed "s|acme-client $web_host && rcctl reload relayd && rcctl restart evrd|acme-client $web_host \&\& $copy_certs \&\& rcctl reload relayd \&\& rcctl reload evrd|" | crontab -
	say "certificate renewal now reloads evrd instead of restarting it"
fi

# --- start -------------------------------------------------------------------
rcctl enable evrd >/dev/null
if [ "$holder" = yes ]; then
	rcctl enable evrd_links >/dev/null
	rcctl order evrd_links evrd >/dev/null # the holder first at boot
fi
if [ -n "$admin" ]; then
	# printf is a shell builtin: the password never shows up in ps
	printf '%s\n' "$pw" | /usr/local/bin/evrctl -config "$conf" user add -admin "$admin" - ||
		say "couldn't make $admin (try: evrctl -config $conf user add -admin $admin -)"
	pw=
fi
if [ "$start" = yes ]; then
	if [ "$holder" = yes ]; then
		if [ "$links_running" = no ]; then
			rcctl start evrd_links >/dev/null
		elif [ "$links_changed" = yes ]; then
			# evrd reconnects everyone through the new holder
			say "the connection holder changed: restarting it (IRC connections drop once)"
			rcctl restart evrd_links >/dev/null
			changed=yes
		fi
		rcctl check evrd_links >/dev/null || say "evrd-links didn't start — evrd connects directly (see /var/log/daemon)"
	fi
	if [ "$running" = yes ] && [ "$changed" = no ] && [ "$writeconf" = no ]; then
		say "evrd itself is unchanged — the web app is updated, no restart (nobody disconnected)"
	elif [ "$running" = yes ]; then
		grep -q '^links_socket' "$conf" && say "restarting evrd — the holder keeps everyone connected"
		rcctl restart evrd >/dev/null
	else
		rcctl start evrd >/dev/null
	fi
	sleep 1
	rcctl check evrd >/dev/null || die "evrd didn't start — see /var/log/daemon"
	say "evrd is running"
fi

echo
case $front in
evrd|relayd)
	url=https://$web_host
	[ "$https_port" = 443 ] || url=$url:$https_port
	echo "Web app:     $url"
	;;
*) echo "Web app:     http://$web_host:$web_port   (plain HTTP)" ;;
esac
case $front/$certs in
evrd/letsencrypt)
	echo "Certificate: Let's Encrypt — evrd gets it within minutes; see Admin → Settings → HTTPS & TLS" ;;
*/selfsigned)
	echo "Certificate: self-signed — browsers and IRC clients will ask you to accept it" ;;
relayd/fallback)
	echo "Certificate: self-signed for now. Once $web_host points here and port 80 is open:"
	echo "             acme-client -F $web_host && $copy_certs && rcctl reload relayd && rcctl reload evrd" ;;
esac
echo "IRC clients: $irc_host port $irc_port (TLS), server password yourname/network:password"
echo "Firewall:    open the ports in $examples/pf.conf"
[ -n "$admin" ] || [ "$writeconf" = no ] || echo "Make an admin: evrctl -config $conf user add -admin yourname -"
}

main "$@"; exit
