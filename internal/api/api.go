// Package api exposes evrd's HTTP + WebSocket admin/client API, consumed by the
// web app: login, network CRUD, connect/disconnect, message history, and a live
// per-network message stream.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/bcrypt"

	"code.ircforever.org/ircforever/evrd/internal/bouncer"
	"code.ircforever.org/ircforever/evrd/internal/store"
)

type Server struct {
	b   *bouncer.Bouncer
	log *slog.Logger
	up  websocket.Upgrader

	mu     sync.Mutex
	tokens map[string]int64 // bearer token -> user id
}

func NewServer(b *bouncer.Bouncer, log *slog.Logger) *Server {
	return &Server{
		b:      b,
		log:    log,
		tokens: map[string]int64{},
		up:     websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
	}
}

// ListenAndServe runs the API until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.handler()}
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()
	s.log.Info("api listening", "addr", addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("GET /api/networks", s.auth(s.handleListNetworks))
	mux.HandleFunc("POST /api/networks", s.auth(s.handleCreateNetwork))
	mux.HandleFunc("DELETE /api/networks/{id}", s.auth(s.handleDeleteNetwork))
	mux.HandleFunc("POST /api/networks/{id}/connect", s.auth(s.handleConnect))
	mux.HandleFunc("POST /api/networks/{id}/disconnect", s.auth(s.handleDisconnect))
	mux.HandleFunc("GET /api/networks/{id}/messages", s.auth(s.handleMessages))
	mux.HandleFunc("GET /api/networks/{id}/stream", s.handleStream) // auth via ?token
	return cors(mux)
}

// --- auth ----------------------------------------------------------------

type ctxKey int

const userKey ctxKey = 0

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := s.userFor(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userKey, uid)))
	}
}

func (s *Server) userFor(token string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	uid, ok := s.tokens[token]
	return uid, ok
}

func userID(r *http.Request) int64 {
	v, _ := r.Context().Value(userKey).(int64)
	return v
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct{ Username, Password string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	u, err := s.b.Store().UserByUsername(req.Username)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)) != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	token := newToken()
	s.mu.Lock()
	s.tokens[token] = u.ID
	s.mu.Unlock()
	writeJSON(w, map[string]any{"token": token, "user": u.Username})
}

// --- networks ------------------------------------------------------------

func (s *Server) handleListNetworks(w http.ResponseWriter, r *http.Request) {
	nets, err := s.b.Store().ListNetworks(userID(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]map[string]any, 0, len(nets))
	for _, n := range nets {
		out = append(out, s.networkDTO(n))
	}
	writeJSON(w, out)
}

func (s *Server) handleCreateNetwork(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name, Host                        string
		Port                              int
		TLS                               bool
		Nick, Username, Realname, Password string
		Channels                          []string
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.Port == 0 {
		req.Port = 6697
	}
	n, err := s.b.Store().CreateNetwork(store.Network{
		UserID: userID(r), Name: req.Name, Host: req.Host, Port: req.Port, TLS: req.TLS,
		Nick: req.Nick, Username: req.Username, Realname: req.Realname,
		Password: req.Password, Channels: req.Channels, Enabled: false,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, s.networkDTO(n))
}

func (s *Server) handleDeleteNetwork(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if _, ok := s.ownedNetwork(w, r, id); !ok {
		return
	}
	s.b.Disconnect(id)
	if err := s.b.Store().DeleteNetwork(userID(r), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if _, ok := s.ownedNetwork(w, r, id); !ok {
		return
	}
	if err := s.b.Connect(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"status": string(s.b.Status(id))})
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if _, ok := s.ownedNetwork(w, r, id); !ok {
		return
	}
	s.b.Disconnect(id)
	writeJSON(w, map[string]any{"status": string(s.b.Status(id))})
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if _, ok := s.ownedNetwork(w, r, id); !ok {
		return
	}
	target := r.URL.Query().Get("target")
	msgs, err := s.b.Store().RecentMessages(id, target, 200)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, messageDTO(m))
	}
	writeJSON(w, out)
}

// handleStream upgrades to a WebSocket: it pushes live messages and accepts
// {target,text} frames to send. Auth is via the ?token query param (browsers
// can't set headers on a WS handshake).
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.userFor(r.URL.Query().Get("token"))
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := pathID(r)
	n, err := s.b.Store().NetworkByID(id)
	if err != nil || n.UserID != uid {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	conn, ok := s.b.ConnFor(id)
	if !ok {
		http.Error(w, "network not connected", http.StatusConflict)
		return
	}

	ws, err := s.up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	sub, unsub := conn.Subscribe()
	defer unsub()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			var in struct{ Target, Text string }
			if err := ws.ReadJSON(&in); err != nil {
				return
			}
			if in.Target != "" && in.Text != "" {
				conn.SendMessage(in.Target, in.Text)
			}
		}
	}()

	for {
		select {
		case m, ok := <-sub:
			if !ok {
				return
			}
			if err := ws.WriteJSON(messageDTO(m)); err != nil {
				return
			}
		case <-done:
			return
		}
	}
}

// --- helpers -------------------------------------------------------------

func (s *Server) ownedNetwork(w http.ResponseWriter, r *http.Request, id int64) (store.Network, bool) {
	n, err := s.b.Store().NetworkByID(id)
	if err != nil || n.UserID != userID(r) {
		http.Error(w, "not found", http.StatusNotFound)
		return store.Network{}, false
	}
	return n, true
}

func (s *Server) networkDTO(n store.Network) map[string]any {
	return map[string]any{
		"id": n.ID, "name": n.Name, "host": n.Host, "port": n.Port,
		"tls": n.TLS, "nick": n.Nick, "username": n.Username,
		"realname": n.Realname, "channels": n.Channels,
		"status": string(s.b.Status(n.ID)),
	}
}

func messageDTO(m store.Message) map[string]any {
	return map[string]any{
		"target": m.Target, "sender": m.Sender, "kind": m.Kind,
		"text": m.Text, "time": m.Time.Unix(),
	}
}

func pathID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}

func newToken() string {
	b := make([]byte, 24)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
