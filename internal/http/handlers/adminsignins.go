package handlers

import (
	"net/http"
	"strconv"
)

// AdminIRCSignIns is GET /api/admin/irc-signins: recent IRC sign-ins,
// newest first — what shape each client's PASS had (never what it sent),
// whose account it was and how it went. ?user= narrows it to one account,
// or "unknown" to the ones nobody could be found for; ?limit= up to 500.
func (s *Handlers) AdminIRCSignIns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 100
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
		limit = min(n, 500)
	}
	var userID int64
	var only map[string]any
	switch v := q.Get("user"); v {
	case "":
	case "unknown":
		userID = -1
	default:
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			writeErr(w, http.StatusBadRequest, "bad user")
			return
		}
		u, err := s.acc.UserByID(r.Context(), id)
		if err != nil {
			writeErr(w, http.StatusNotFound, "no such user")
			return
		}
		userID, only = id, map[string]any{"id": u.ID, "username": u.Username}
	}
	rows, err := s.b.SignIns(r.Context(), userID, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	users, _ := s.acc.ListUsers(r.Context())
	names := map[int64]string{}
	for _, u := range users {
		names[u.ID] = u.Username
	}
	out := make([]map[string]any, 0, len(rows))
	for _, x := range rows {
		row := map[string]any{"at": x.At.Unix(), "ip": x.IP, "nick": x.Nick, "ident": x.Ident, "format": x.Format,
			"full": x.Format == fullPass, "network": x.Network, "result": x.Result, "matched": x.Matched, "passwordLength": x.PasswordLen}
		if x.UserID != 0 {
			row["userId"], row["user"] = x.UserID, orDeleted(names[x.UserID])
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"signIns": out, "user": only})
}

// fullPass is the PASS shape that names everything (client.fullLogin).
const fullPass = "<username>/<network>:<password>"
