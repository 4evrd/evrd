package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/links"
	"github.com/4evrd/evrd/internal/settings"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/testirc"
	"github.com/4evrd/evrd/internal/version"
)

const testKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

// syncBuffer is a bytes.Buffer safe to write from evrd's goroutines while
// the test reads it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// noEnv keeps the developer's environment out of evrd's config.
func noEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{
		"EVRD_CONFIG", "EVRD_UPSTREAM_BIND", "EVRD_UPSTREAM_PREFIX6", "EVRD_DB", "EVRD_HISTORY",
		"EVRD_OIDC_CLIENT_SECRET", "EVRD_CLUSTER_SECRET", "EVRD_SECRET_KEY", "EVRD_SECRET_KEY_FILE",
		"EVRD_PROVISION_TOKEN", "EVRD_LINKS_SOCKET", "EVRD_NODE_ID", "EVRD_INTERNAL_URL",
		"EVRD_INTERNAL_IRC_ADDR", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "EVRD_METRICS_URL",
		"EVRD_NETWORK_DIRECTORY", "EVRD_IDENTD_LISTEN",
	} {
		t.Setenv(v, "")
	}
}

// freeAddr is a loopback address with a port nothing listens on now.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// busyAddr is a loopback address something listens on until the test ends.
func busyAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String()
}

// shortDir is a temporary directory with a short path, for unix sockets.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "evrd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// writeCert writes a self-signed certificate and its key into dir.
func writeCert(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "evrd test"},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath = filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), 0o600)
	return certPath, keyPath
}

// testConfig writes a config for evrd with its stores in dir, its API and
// an IRC listener on free ports, and extra directives after those.
func testConfig(t *testing.T, dir string, extra ...string) (path, api, irc string) {
	t.Helper()
	api, irc = freeAddr(t), freeAddr(t)
	lines := append([]string{
		"db sqlite:" + filepath.Join(dir, "evrd.db"),
		"history pebble:" + filepath.Join(dir, "history"),
		"api " + api,
		"listen irc+insecure://" + irc,
		"secret_key " + testKey,
		"network_directory off", // tests don't fetch evrd.sh
	}, extra...)
	path = filepath.Join(dir, "evrd.conf")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, api, irc
}

// daemon is evrd running in the test.
type daemon struct {
	t      *testing.T
	stderr *syncBuffer
	cancel context.CancelFunc
	done   chan int
	code   int
	exited bool
}

// startEvrd runs evrd with args and waits for its API at api to answer.
func startEvrd(t *testing.T, api string, args ...string) *daemon {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	d := &daemon{t: t, stderr: &syncBuffer{}, cancel: cancel, done: make(chan int, 1)}
	go func() { d.done <- run(ctx, append([]string{"evrd"}, args...), io.Discard, d.stderr) }()
	t.Cleanup(func() { d.stop() })
	deadline := time.Now().Add(15 * time.Second)
	for {
		select {
		case code := <-d.done:
			t.Fatalf("evrd exited early (%d):\n%s", code, d.stderr)
		default:
		}
		if r, err := http.Get("http://" + api + "/api/config"); err == nil {
			r.Body.Close()
			if r.StatusCode == http.StatusOK {
				return d
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("evrd's API never answered:\n%s", d.stderr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// stop cancels evrd's context and returns its exit status.
func (d *daemon) stop() int {
	d.t.Helper()
	if d.exited {
		return d.code
	}
	d.cancel()
	select {
	case d.code = <-d.done:
		d.exited = true
		return d.code
	case <-time.After(15 * time.Second):
		d.t.Fatalf("evrd didn't stop:\n%s", d.stderr)
		return -1
	}
}

// waitLog waits for evrd to log s.
func (d *daemon) waitLog(s string) {
	d.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(d.stderr.String(), s) {
		if time.Now().After(deadline) {
			d.t.Fatalf("evrd never logged %q:\n%s", s, d.stderr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// apiConfig is GET /api/config.
func apiConfig(t *testing.T, client *http.Client, base string) map[string]any {
	t.Helper()
	r, err := client.Get(base + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var m map[string]any
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

// runEvrd runs evrd to completion.
func runEvrd(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	code = run(ctx, append([]string{"evrd"}, args...), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestFlags(t *testing.T) {
	noEnv(t)
	code, out, errOut := runEvrd(t, "-version")
	if code != 0 || out != "evrd "+version.Version+"\n" || errOut != "" {
		t.Fatalf("-version: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	code, out, errOut = runEvrd(t, "-nope")
	if code != 2 || out != "" || !strings.Contains(errOut, "flag provided but not defined: -nope") || !strings.Contains(errOut, "-config string") {
		t.Fatalf("-nope: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	code, _, errOut = runEvrd(t, "-h")
	if code != 0 || !strings.Contains(errOut, "-version") {
		t.Fatalf("-h: exit %d, stderr %q", code, errOut)
	}
}

// setupUser makes alice (password alice-password) with a network on a
// fake IRC server, before evrd starts.
func setupUser(t *testing.T, dir string) {
	t.Helper()
	if err := store.SetSecretKeys(nil); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open("sqlite:" + filepath.Join(dir, "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u, err := accounts.New(st, config.Default().Limits).Create(accounts.CreateInput{Username: "alice", Password: "alice-password", Role: store.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(testirc.Start(t))
	p, _ := strconv.Atoi(port)
	if _, err := st.CreateNetwork(t.Context(), store.Network{UserID: u.ID, Name: "testnet", Host: host, Port: p, Nick: "alice", Channels: []string{"#evrd"}}); err != nil {
		t.Fatal(err)
	}
}

// ircRegister signs alice in over IRC and returns the welcome line.
func ircRegister(t *testing.T, addr string) string {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprint(c, "PASS alice/testnet:alice-password\r\nNICK alice\r\nUSER alice 0 * :Alice\r\n")
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	sc := bufio.NewScanner(c)
	var seen []string
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) > 1 && f[1] == "001" {
			return sc.Text()
		}
		seen = append(seen, sc.Text())
	}
	t.Fatalf("no welcome; got:\n%s", strings.Join(seen, "\n"))
	return ""
}

// TestServes runs a single node with most things on: privileges "dropped"
// to the current user (no chroot), the connection holder, its own HTTPS
// and HTTP ports, certificate files, the web app, and a live settings
// change.
func TestServes(t *testing.T) {
	noEnv(t)
	settings.PollInterval = 50 * time.Millisecond
	t.Cleanup(func() { settings.PollInterval = 5 * time.Second })

	dir := t.TempDir()
	setupUser(t, dir)
	certPath, keyPath := writeCert(t, dir)
	web := filepath.Join(dir, "web")
	os.MkdirAll(web, 0o755)
	os.WriteFile(filepath.Join(web, "index.html"), []byte("<title>evrd web app</title>"), 0o644)

	// The connection holder, as evrd-links would run it.
	sock := filepath.Join(shortDir(t), "links.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	hctx, hcancel := context.WithCancel(context.Background())
	t.Cleanup(hcancel)
	go links.NewHolder(slog.New(slog.NewTextHandler(io.Discard, nil))).Serve(hctx, ln)

	// The network directory, from local files.
	dirs := [2]string{filepath.Join(dir, "networks-a.json"), filepath.Join(dir, "networks-b.json")}
	for _, p := range dirs {
		os.WriteFile(p, []byte(`{"version": 1, "networks": [{"id": "dev", "name": "Dev", "servers": [{"host": "ircd", "port": 6667}]}]}`), 0o644)
	}

	httpsAddr, httpAddr, identAddr := freeAddr(t), freeAddr(t), freeAddr(t)
	extra := []string{
		"identd_listen " + identAddr,
		"network_directory " + dirs[0],
		"tls_cert " + certPath, "tls_key " + keyPath,
		"https_listen " + httpsAddr, "http_listen " + httpAddr,
		"web_root " + web,
		"links_socket " + sock,
		"upstream_bind 127.0.0.1",
		"irc_host irc.test",
	}
	sandboxed := false
	if me, err := user.Current(); err == nil && me.Uid != "0" {
		// Same user: evrd takes the sandbox path (binding its ports
		// first) without needing root.
		extra = append(extra, "user "+me.Username)
		sandboxed = true
	}
	conf, api, irc := testConfig(t, dir, extra...)
	d := startEvrd(t, api, "-config", conf)

	cfg := apiConfig(t, http.DefaultClient, "http://"+api)
	if cfg["version"] != version.Version || cfg["ircHost"] != "irc.test" || cfg["passwordLogin"] != true || cfg["ircSelfSigned"] != true {
		t.Fatalf("/api/config: %v", cfg)
	}
	if sandboxed && !strings.Contains(d.stderr.String(), "msg=sandboxed") {
		t.Fatalf("not sandboxed:\n%s", d.stderr)
	}

	// The web app, on the API port.
	r, err := http.Get("http://" + api + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if !strings.Contains(string(body), "evrd web app") {
		t.Fatalf("web app: %d %q", r.StatusCode, body)
	}

	// HTTPS with the certificate files, and HTTP sending browsers there.
	insecure := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	r, err = insecure.Get("https://" + httpsAddr + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusOK || r.Header.Get("Strict-Transport-Security") == "" || r.TLS.PeerCertificates[0].Subject.CommonName != "evrd test" {
		t.Fatalf("https: %d %v", r.StatusCode, r.Header)
	}
	r, err = insecure.Get("http://" + httpAddr + "/x")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	_, httpsPort, _ := net.SplitHostPort(httpsAddr)
	if r.StatusCode != http.StatusMovedPermanently || r.Header.Get("Location") != "https://127.0.0.1:"+httpsPort+"/x" {
		t.Fatalf("http: %d %q", r.StatusCode, r.Header.Get("Location"))
	}

	// An IRC client signs in; the network connection goes through the
	// holder.
	if w := ircRegister(t, irc); !strings.Contains(w, "Welcome to evrd - attached to testnet") {
		t.Fatalf("welcome: %q", w)
	}
	hc := links.NewClient(sock)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for i := 0; ; i++ { // the welcome doesn't wait for the server
		ls, err := hc.List(ctx)
		if err == nil && len(ls) == 1 && ls[0].Registered {
			break
		}
		if i == 400 {
			t.Fatalf("holder links: %+v %v", ls, err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// identd answers (nobody here dialed a server on port 1).
	ic, err := net.Dial("tcp", identAddr)
	if err != nil {
		t.Fatal(err)
	}
	ic.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(ic, "1234 , 1\r\n")
	reply, _ := io.ReadAll(ic)
	ic.Close()
	if string(reply) != "1234 , 1 : ERROR : NO-USER\r\n" {
		t.Fatalf("identd: %q", reply)
	}

	// SIGHUP re-reads the certificate files.
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	d.waitLog("reloaded tls_cert")
	os.WriteFile(certPath, []byte("not a certificate"), 0o600)
	syscall.Kill(os.Getpid(), syscall.SIGHUP)
	d.waitLog("reload certificate")

	// A setting changed in the database (as on another node) applies live.
	st, err := store.Open("sqlite:" + filepath.Join(dir, "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	err = st.SaveSettings(ctx, map[string]string{"irc_host": "irc.changed", "metrics_url": "http://metrics.invalid", "network_directory": dirs[1]}, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for apiConfig(t, http.DefaultClient, "http://"+api)["ircHost"] != "irc.changed" {
		if time.Now().After(deadline) {
			t.Fatalf("setting never applied:\n%s", d.stderr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// ...the directory too: read from the new file, and a copy stored
	for {
		if c, _ := st.Cached(ctx, "network-directory"); strings.Contains(c, dirs[1]) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("directory never moved:\n%s", d.stderr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	st.Close()

	if code := d.stop(); code != 0 {
		t.Fatalf("exit %d:\n%s", code, d.stderr)
	}
	if _, err := http.Get("http://" + api + "/api/config"); err == nil {
		t.Fatal("API still up after stopping")
	}
	// The holder kept the network connection through evrd stopping.
	if ls, err := hc.List(ctx); err != nil || len(ls) != 1 {
		t.Fatalf("holder links after stop: %+v %v", ls, err)
	}
}

// TestCluster runs a node with cluster settings, metrics export and
// password sign-in off.
func TestCluster(t *testing.T) {
	noEnv(t)
	var mu sync.Mutex
	var auth []string
	otlp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
	}))
	defer otlp.Close()
	otlpURL := strings.Replace(otlp.URL, "http://", "http://vm:s3cret@", 1) + "/v1/metrics"

	dir := t.TempDir()
	internal, internalIRC := freeAddr(t), freeAddr(t)
	conf, api, _ := testConfig(t, dir,
		"internal_listen "+internal,
		"internal_url http://"+internal,
		"internal_irc_listen "+internalIRC,
		"internal_irc_addr "+internalIRC,
		"cluster_secret a-cluster-secret",
		"cluster_lease 30s",
		"node_id node-a",
		"links_socket /nonexistent/links.sock",
		"settings_write_conf yes",
		"password_login no",
		"otel_endpoint "+otlpURL,
		"otel_interval 50ms",
	)
	d := startEvrd(t, api, "-config", conf)
	if cfg := apiConfig(t, http.DefaultClient, "http://"+api); cfg["passwordLogin"] != false {
		t.Fatalf("/api/config: %v", cfg)
	}
	// The internal ports are up.
	for _, a := range []string{internal, internalIRC} {
		c, err := net.Dial("tcp", a)
		if err != nil {
			t.Fatalf("internal port %s: %v", a, err)
		}
		c.Close()
	}
	if code := d.stop(); code != 0 {
		t.Fatalf("exit %d:\n%s", code, d.stderr)
	}
	log := d.stderr.String()
	for _, want := range []string{
		"links_socket is for a single node",
		"settings_write_conf is on in a cluster",
		"nobody can sign in to the web app",
		"cluster: history is on local disk",
		"msg=\"cluster member\" node=node-a",
		"exporting metrics",
		"vm:xxxxx@",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q", want)
		}
	}
	if strings.Contains(log, "s3cret") {
		t.Error("the metrics password was logged")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(auth) == 0 || auth[0] != "Basic dm06czNjcmV0" { // vm:s3cret
		t.Errorf("metrics pushes: %q", auth)
	}
}

// TestStartupErrors checks each startup failure is logged and exits 1.
func TestStartupErrors(t *testing.T) {
	noEnv(t)
	root := os.Geteuid() == 0
	me, _ := user.Current()
	oidc404 := httptest.NewServer(http.NotFoundHandler())
	defer oidc404.Close()

	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, dir string) []string // the config's extra lines
		env   map[string]string
		want  string
		skip  bool
	}{
		{name: "environment", env: map[string]string{"EVRD_UPSTREAM_BIND": "not-an-ip"}, want: "msg=environment err=\"EVRD_UPSTREAM_BIND:"},
		{name: "secret key", setup: lines("secret_key short"), want: "msg=\"secret key\""},
		{name: "tls files", setup: lines("tls files", "tls_cert /nonexistent/tls.crt", "tls_key /nonexistent/tls.key"), want: "msg=tls_cert"},
		{name: "store", setup: lines("db sqlite:/nonexistent/dir/evrd.db"), want: "msg=\"open store\" dsn=sqlite:/nonexistent/dir/evrd.db"},
		{name: "settings", setup: func(t *testing.T, dir string) []string {
			st, err := store.Open("sqlite:" + filepath.Join(dir, "evrd.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if err := st.SaveSettings(t.Context(), map[string]string{"max_networks": "lots"}, nil, "test"); err != nil {
				t.Fatal(err)
			}
			return nil
		}, want: "msg=\"load settings\""},
		{name: "tls acme", setup: lines("tls acme", "irc_host localhost"), want: "msg=tls "},
		{name: "history", setup: lines("history nope://x"), want: "msg=\"open history\" store=nope://x"},
		{name: "sign-in", setup: lines("oidc_issuer "+oidc404.URL, "public_url https://bnc.test"), want: "msg=\"set up sign-in\" issuer=" + oidc404.URL},
		{name: "cluster", setup: lines("internal_listen 127.0.0.1:1"), want: "cluster: internal_listen needs cluster_secret"},
		{name: "outbound", setup: lines("upstream_prefix6 10.0.0.0/8"), want: "msg=outbound"},
		{name: "api port", setup: func(t *testing.T, dir string) []string {
			return []string{"api " + busyAddr(t)}
		}, want: "msg=\"api server\""},
		{name: "https port", setup: func(t *testing.T, dir string) []string {
			return []string{"https_listen " + busyAddr(t)}
		}, want: "msg=https"},
		{name: "http port", setup: func(t *testing.T, dir string) []string {
			return []string{"http_listen " + busyAddr(t)}
		}, want: "msg=http "},
		{name: "identd port", setup: func(t *testing.T, dir string) []string {
			return []string{"identd_listen " + busyAddr(t)}
		}, want: "msg=identd"},
		{name: "internal api port", setup: func(t *testing.T, dir string) []string {
			return []string{"internal_listen " + busyAddr(t), "internal_url http://x", "internal_irc_listen " + freeAddr(t), "internal_irc_addr x:1", "cluster_secret s"}
		}, want: "msg=\"internal api\""},
		// The sandbox: privileged ports are bound, and paths checked, before
		// anything is confined.
		{name: "prebind", skip: root || me == nil, setup: func(t *testing.T, dir string) []string {
			return []string{"user " + me.Username, "http_listen " + busyAddr(t)}
		}, want: "msg=listen"},
		{name: "jail", setup: func(t *testing.T, dir string) []string {
			return []string{"chroot " + dir, "db sqlite:relative.db"}
		}, want: "msg=chroot"},
		{name: "links outside chroot", setup: func(t *testing.T, dir string) []string {
			return []string{"chroot " + dir, "links_socket /elsewhere/links.sock"}
		}, want: "links_socket must be inside the chroot"},
		{name: "chroot needs root", skip: root, setup: func(t *testing.T, dir string) []string {
			crt, key := writeCert(t, dir)
			return []string{"chroot " + dir, "links_socket " + filepath.Join(dir, "links.sock"), "web_root " + dir, "tls_cert " + crt, "tls_key " + key}
		}, want: "chroot and user need evrd to be started as root"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skip {
				t.Skip("not as root, or no current user")
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			dir := t.TempDir()
			var extra []string
			if tc.setup != nil {
				extra = tc.setup(t, dir)
			}
			conf, _, _ := testConfig(t, dir, extra...)
			code, out, errOut := runEvrd(t, "-config", conf)
			if code != 1 || out != "" || !strings.Contains(errOut, "level=ERROR") || !strings.Contains(errOut, tc.want) {
				t.Fatalf("exit %d, stdout %q, stderr:\n%s\nwant exit 1 and %q", code, out, errOut, tc.want)
			}
		})
	}
}

// lines is a setup that adds fixed config lines.
func lines(l ...string) func(*testing.T, string) []string {
	return func(*testing.T, string) []string { return l }
}

func TestConfigFile(t *testing.T) {
	noEnv(t)
	missing := filepath.Join(t.TempDir(), "missing.conf")
	code, _, errOut := runEvrd(t, "-config", missing)
	if code != 1 || !strings.Contains(errOut, "msg=\"load config\" path="+missing) || !strings.Contains(errOut, "no such file or directory") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	// $EVRD_CONFIG is the default for -config.
	t.Setenv("EVRD_CONFIG", missing)
	code, _, errOut = runEvrd(t)
	if code != 1 || !strings.Contains(errOut, "path="+missing) {
		t.Fatalf("$EVRD_CONFIG: exit %d, stderr %q", code, errOut)
	}
	t.Setenv("EVRD_CONFIG", "")

	bad := filepath.Join(t.TempDir(), "bad.conf")
	os.WriteFile(bad, []byte("listen irc+insecure://127.0.0.1:0\nfrobnicate yes\n"), 0o600)
	code, _, errOut = runEvrd(t, "-config", bad)
	if code != 1 || !strings.Contains(errOut, bad+":2: frobnicate: unknown directive") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

// TestNoSecretKey warns that saved passwords aren't encrypted.
func TestNoSecretKey(t *testing.T) {
	noEnv(t)
	dir := t.TempDir()
	api := freeAddr(t)
	conf := filepath.Join(dir, "evrd.conf")
	os.WriteFile(conf, []byte(strings.Join([]string{
		"db sqlite:" + filepath.Join(dir, "evrd.db"),
		"history pebble:" + filepath.Join(dir, "history"),
		"api " + api,
		"listen irc+insecure://" + freeAddr(t),
	}, "\n")), 0o600)
	d := startEvrd(t, api, "-config", conf)
	if code := d.stop(); code != 0 {
		t.Fatalf("exit %d:\n%s", code, d.stderr)
	}
	if !strings.Contains(d.stderr.String(), "no secret_key set: network passwords are stored unencrypted") {
		t.Fatalf("no warning:\n%s", d.stderr)
	}
}

func TestLocalPathAndRedact(t *testing.T) {
	for _, tc := range []struct {
		dsn, scheme, path string
		ok                bool
	}{
		{"sqlite:/var/evrd/evrd.db?_pragma=x", "sqlite:", "/var/evrd/evrd.db", true},
		{"/var/evrd/evrd.db", "sqlite:", "/var/evrd/evrd.db", true},
		{"pebble:/var/evrd/history", "pebble:", "/var/evrd/history", true},
		{"postgres://u:p@db/evrd", "sqlite:", "", false},
		{"", "sqlite:", "", false},
		{"sqlite:", "sqlite:", "", false},
	} {
		if p, ok := localPath(tc.dsn, tc.scheme); p != tc.path || ok != tc.ok {
			t.Errorf("localPath(%q) = %q, %v", tc.dsn, p, ok)
		}
	}
	if got := redact("postgres://evrd:secret@db/evrd"); got != "postgres://evrd:xxxxx@db/evrd" {
		t.Errorf("redact: %q", got)
	}
	if got := redact("pebble:/var/evrd/history"); got != "pebble:/var/evrd/history" {
		t.Errorf("redact: %q", got)
	}
	for src, want := range map[string]string{"/etc/evrd/n.json": "/etc/evrd/n.json", "file:///etc/n.json": "/etc/n.json", "https://evrd.sh/x.json": "", "": ""} {
		if p, ok := directoryFile(src); p != want || ok != (want != "") {
			t.Errorf("directoryFile(%q) = %q, %v", src, p, ok)
		}
	}
}
