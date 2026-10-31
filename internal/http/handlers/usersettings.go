package handlers

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/store"
)

// userSettingsInput is the ZNC-style per-user bouncer settings, editable by
// the user (PATCH /api/me) and by admins. Nil fields are unchanged.
type userSettingsInput struct {
	DefaultIdent         *string            `json:"defaultIdent"`
	QuitMsg              *string            `json:"quitMsg"`
	BindHost             *string            `json:"bindHost"`
	PlaybackLines        *int               `json:"playbackLines"`
	AutoClearChanBuffer  *bool              `json:"autoClearChanBuffer"`
	AutoClearQueryBuffer *bool              `json:"autoClearQueryBuffer"`
	PrependTimestamp     *bool              `json:"prependTimestamp"`
	AppendTimestamp      *bool              `json:"appendTimestamp"`
	TimestampFormat      *string            `json:"timestampFormat"`
	Timezone             *string            `json:"timezone"`
	QuietRejoin          *bool              `json:"quietRejoin"`
	CTCPReplies          *map[string]string `json:"ctcpReplies"`
	AllowedHosts         *[]string          `json:"allowedHosts"`
}

// maxPlaybackLines caps PlaybackLines.
const maxPlaybackLines = 500

func (in userSettingsInput) apply(u *store.User) error {
	bad := func(msg string) error { return &accounts.ValidationError{Message: msg} }
	if in.DefaultIdent != nil {
		v := strings.TrimSpace(*in.DefaultIdent)
		if strings.ContainsAny(v, " @!:") || utf8.RuneCountInString(v) > 30 {
			return bad("the ident can't contain spaces, @, ! or : and is at most 30 characters")
		}
		u.DefaultIdent = v
	}
	if in.QuitMsg != nil {
		v := strings.TrimSpace(*in.QuitMsg)
		if strings.ContainsAny(v, "\r\n") || utf8.RuneCountInString(v) > 300 {
			return bad("the quit message is one line, up to 300 characters")
		}
		u.QuitMsg = v
	}
	if in.BindHost != nil {
		u.BindHost = strings.TrimSpace(*in.BindHost)
	}
	if in.PlaybackLines != nil {
		if *in.PlaybackLines < 0 || *in.PlaybackLines > maxPlaybackLines {
			return bad("playback is 0–500 lines")
		}
		u.PlaybackLines = *in.PlaybackLines
	}
	set := func(dst *bool, v *bool) {
		if v != nil {
			*dst = *v
		}
	}
	set(&u.AutoClearChanBuffer, in.AutoClearChanBuffer)
	set(&u.AutoClearQueryBuffer, in.AutoClearQueryBuffer)
	set(&u.PrependTimestamp, in.PrependTimestamp)
	set(&u.AppendTimestamp, in.AppendTimestamp)
	set(&u.QuietRejoin, in.QuietRejoin)
	if in.TimestampFormat != nil {
		v := strings.TrimSpace(*in.TimestampFormat)
		if v == "" || len(v) > 64 || strings.ContainsAny(v, "\r\n") {
			return bad("the timestamp format is one line, up to 64 characters (e.g. [%H:%M:%S])")
		}
		u.TimestampFormat = v
	}
	if in.Timezone != nil {
		v := strings.TrimSpace(*in.Timezone)
		if v != "" {
			if _, err := time.LoadLocation(v); err != nil {
				return bad("“" + v + "” isn't a time zone — use a name like Europe/Berlin or UTC")
			}
		}
		u.Timezone = v
	}
	if in.CTCPReplies != nil {
		m := map[string]string{}
		for k, v := range *in.CTCPReplies {
			k = strings.ToUpper(strings.TrimSpace(k))
			if k == "" {
				continue
			}
			if strings.Trim(k, "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") != "" || len(k) > 20 {
				return bad("CTCP requests are single words, like VERSION or TIME")
			}
			if k == "ACTION" {
				return bad("ACTION is /me, not a CTCP request evrd can answer")
			}
			if strings.ContainsAny(v, "\r\n\x01") || utf8.RuneCountInString(v) > 200 {
				return bad("a CTCP reply is one line, up to 200 characters")
			}
			m[k] = strings.TrimSpace(v)
		}
		if len(m) > 20 {
			return bad("keep it to 20 CTCP replies")
		}
		u.CTCPReplies = m
	}
	if in.AllowedHosts != nil {
		hosts, err := accounts.NormalizeAllowedHosts(*in.AllowedHosts)
		if err != nil {
			return err
		}
		u.AllowedHosts = hosts
	}
	return nil
}

// settingsDTO is the user's bouncer settings.
func settingsDTO(u store.User) map[string]any {
	ctcp := u.CTCPReplies
	if ctcp == nil {
		ctcp = map[string]string{}
	}
	return map[string]any{
		"defaultIdent": u.DefaultIdent, "quitMsg": u.QuitMsg, "bindHost": u.BindHost,
		"denySetBindHost": u.DenySetBindHost,
		"playbackLines":   u.PlaybackLines, "autoClearChanBuffer": u.AutoClearChanBuffer,
		"autoClearQueryBuffer": u.AutoClearQueryBuffer, "prependTimestamp": u.PrependTimestamp,
		"appendTimestamp": u.AppendTimestamp, "timestampFormat": u.TimestampFormat, "timezone": u.Timezone,
		"quietRejoin": u.QuietRejoin, "ctcpReplies": ctcp, "allowedHosts": orEmpty(u.AllowedHosts),
	}
}
