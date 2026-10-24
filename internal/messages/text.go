package messages

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Text is something evrd says, as a key and its arguments: kept that way
// (a log entry, an event, an error), it's put in each reader's language
// when shown. Args are text; Sub are arguments that are texts of evrd's
// own, put in the reader's language too. A Text is stored as JSON.
type Text struct {
	Key  string            `json:"key,omitempty"`
	Args map[string]string `json:"args,omitempty"`
	Sub  map[string]Text   `json:"sub,omitempty"`
}

// T is the Text for key (irc.<key>) with name/value pairs of arguments. A
// Text argument stays one (it's shown in the reader's language), an error
// that carries one (*Error) too; anything else is its text.
func T(key string, args ...any) Text {
	t := Text{Key: Area + key}
	for i := 0; i+1 < len(args); i += 2 {
		name := fmt.Sprint(args[i])
		if sub, ok := asText(args[i+1]); ok {
			if t.Sub == nil {
				t.Sub = map[string]Text{}
			}
			t.Sub[name] = sub
			continue
		}
		if t.Args == nil {
			t.Args = map[string]string{}
		}
		t.Args[name] = fmt.Sprint(args[i+1])
	}
	return t
}

// Raw is text evrd passes on as it is (a server's words, an error it
// doesn't know): the same in every language.
func Raw(text string) Text { return T("raw", "text", text) }

// Texter is an error (or anything) that says what it is in words.
type Texter interface{ MessageText() Text }

// asText is v as a Text: a Text, or an error that is (or wraps) a Texter
// with one.
func asText(v any) (Text, bool) {
	switch v := v.(type) {
	case Text:
		return v, !v.IsZero()
	case error:
		var tx Texter
		if errors.As(v, &tx) {
			t := tx.MessageText()
			return t, !t.IsZero()
		}
	}
	return Text{}, false
}

// English is t in English.
func (t Text) English() string { return English.Text(t) }

// IsZero reports whether t says nothing.
func (t Text) IsZero() bool { return t.Key == "" }

// Text is t in p's language ("" for a zero Text).
func (p *Printer) Text(t Text) string {
	if t.IsZero() {
		return ""
	}
	args := make([]any, 0, 2*(len(t.Args)+len(t.Sub)))
	for k, v := range t.Args {
		args = append(args, k, v)
	}
	for k, v := range t.Sub {
		args = append(args, k, p.Text(v))
	}
	return p.T(t.Key, args...)
}

// Error is an error evrd explains to the user: Error is its English, and
// it's shown in the reader's language where there is one.
type Error struct{ Text Text }

// Errorf is the Error for key (irc.<key>) with name/value pairs of
// arguments (as T).
func Errorf(key string, args ...any) *Error { return &Error{T(key, args...)} }

func (e *Error) Error() string { return e.Text.English() }

// MessageText is e in words.
func (e *Error) MessageText() Text { return e.Text }

// TextOf is what err says in words: its Text when it has one (a Texter,
// or wrapping one), else its message as it is.
func TextOf(err error) Text {
	if err == nil {
		return Text{}
	}
	if t, ok := asText(err); ok {
		return t
	}
	return Raw(err.Error())
}

// Say is err in p's language where it says what it is in words, else its
// message.
func (p *Printer) Say(err error) string {
	if err == nil {
		return ""
	}
	if t, ok := asText(err); ok {
		return p.Text(t)
	}
	return err.Error()
}

// Sentence is text as a sentence of its own: a capital first letter, and
// a full stop unless it ends in one ("。", "!" and "?" count).
func Sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	s = string(unicode.ToUpper(r)) + s[n:]
	if last, _ := utf8.DecodeLastRuneInString(s); !strings.ContainsRune(".。!?！？", last) {
		s += "."
	}
	return s
}
