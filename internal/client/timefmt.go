package client

import (
	"fmt"
	"strings"
	"time"
)

// timestamp formats t for playback text, in the user's format and zone.
func (se *session) timestamp(t time.Time) string {
	loc := time.Local
	if se.acct.Timezone != "" {
		if l, err := time.LoadLocation(se.acct.Timezone); err == nil {
			loc = l
		}
	}
	return strftime(orElse(se.acct.TimestampFormat, "[%H:%M:%S]"), t.In(loc))
}

// strftime supports the conversions ZNC users put in timestamp formats.
func strftime(f string, t time.Time) string {
	var b strings.Builder
	for i := 0; i < len(f); i++ {
		if f[i] != '%' || i == len(f)-1 {
			b.WriteByte(f[i])
			continue
		}
		i++
		switch f[i] {
		case 'H':
			fmt.Fprintf(&b, "%02d", t.Hour())
		case 'I':
			h := t.Hour() % 12
			if h == 0 {
				h = 12
			}
			fmt.Fprintf(&b, "%02d", h)
		case 'M':
			fmt.Fprintf(&b, "%02d", t.Minute())
		case 'S':
			fmt.Fprintf(&b, "%02d", t.Second())
		case 'p':
			b.WriteString(t.Format("PM"))
		case 'Y':
			fmt.Fprintf(&b, "%04d", t.Year())
		case 'y':
			fmt.Fprintf(&b, "%02d", t.Year()%100)
		case 'm':
			fmt.Fprintf(&b, "%02d", int(t.Month()))
		case 'd':
			fmt.Fprintf(&b, "%02d", t.Day())
		case 'e':
			fmt.Fprintf(&b, "%2d", t.Day())
		case 'b', 'h':
			b.WriteString(t.Format("Jan"))
		case 'B':
			b.WriteString(t.Format("January"))
		case 'a':
			b.WriteString(t.Format("Mon"))
		case 'A':
			b.WriteString(t.Format("Monday"))
		case 'j':
			fmt.Fprintf(&b, "%03d", t.YearDay())
		case 'Z':
			b.WriteString(t.Format("MST"))
		case 'z':
			b.WriteString(t.Format("-0700"))
		case 'F':
			b.WriteString(t.Format("2006-01-02"))
		case 'T':
			b.WriteString(t.Format("15:04:05"))
		case 'R':
			b.WriteString(t.Format("15:04"))
		case 's':
			fmt.Fprintf(&b, "%d", t.Unix())
		case '%':
			b.WriteByte('%')
		default:
			b.WriteByte('%')
			b.WriteByte(f[i])
		}
	}
	return b.String()
}
