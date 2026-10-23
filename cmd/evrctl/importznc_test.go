package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/store"
)

func TestImportZNC(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.DB = "sqlite:" + filepath.Join(dir, "evrd.db")
	cfg.History = "pebble:" + filepath.Join(dir, "history")
	cfg.Secrets.Key = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

	st, err := store.Open(cfg.DB)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := st.CreateUser(t.Context(), store.User{Username: "akadmin"})
	n, _ := st.CreateNetwork(t.Context(), store.Network{UserID: u.ID, Name: "libera"})
	st.Close()

	znc := filepath.Join(dir, "znc", "users", "admin")
	write := func(rel, body string) {
		p := filepath.Join(znc, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	write("moddata/log/libera/#go/2024-03-09.log", "[10:00:00] <bob> hi\n[10:00:05] *** Joins: carol (c@h)\n[10:00:06] Connected to IRC (x)\n")
	write("moddata/log/Libera/bob/2024-03-10.log", "[11:00:00] <bob> a query\n")
	write("moddata/log/gone/#x/2024-03-10.log", "[11:00:00] <bob> a deleted network\n")

	c := &ctl{stdout: io.Discard, stderr: io.Discard}
	if err := c.importZNC(cfg, []string{"akadmin", znc}); err != nil {
		t.Fatal(err)
	}

	h, err := history.Open(cfg.History)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	msgs, _ := h.Recent(n.ID, "#go", 10)
	if len(msgs) != 2 || msgs[0].Text != "hi" || msgs[1].Kind != "join" || msgs[1].Sender != "carol" {
		t.Fatalf("#go: %+v", msgs)
	}
	if q, _ := h.Recent(n.ID, "bob", 10); len(q) != 1 { // network names match case-insensitively
		t.Fatalf("query: %+v", q)
	}
	st, _ = store.Open(cfg.DB)
	defer st.Close()
	u, _ = st.UserByUsername(t.Context(), "akadmin")
	if u.BacklogLines == nil || *u.BacklogLines != 0 {
		t.Fatalf("backlog not unlimited: %v", u.BacklogLines)
	}
}
