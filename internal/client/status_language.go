package client

import (
	"context"
	"strings"

	"github.com/4evrd/evrd/internal/messages"
)

// cmdLanguage shows the account's language and the ones to pick from, or
// picks one: the web app's and *status's (- for none: the browser's on the
// web, English here).
func (se *session) cmdLanguage(args []string) {
	if len(args) > 1 {
		se.say("usage.language")
		return
	}
	u := se.account()
	if u.ID == 0 {
		se.say("accountFailedPlain")
		return
	}
	choices := se.languageChoices()
	if len(args) == 0 {
		if c, ok := findLanguage(choices, u.Language); ok {
			se.say("language.current", "language", languageLabel(c))
		} else {
			se.say("language.none")
		}
		se.say("language.choices", "languages", languageList(choices))
		return
	}
	code := ""
	if args[0] != "-" {
		c, ok := findLanguage(choices, args[0])
		if !ok {
			se.say("language.unknown", "language", args[0], "languages", languageList(choices))
			return
		}
		code = c.Code
	}
	u.Language = code
	if err := se.srv.acc.UpdateUser(context.Background(), u); err != nil {
		se.say("saveFailed", "error", err)
		return
	}
	se.acct.Language = code
	se.pr, se.lang = nil, "" // answer in the new one
	if c, ok := findLanguage(choices, code); ok {
		se.say("language.set", "language", languageLabel(c))
	} else {
		se.say("language.cleared")
	}
}

// languageChoices are the languages the user can pick (the built-in ones
// without a messages service).
func (se *session) languageChoices() []messages.Choice {
	if se.srv.msgs != nil {
		if cs, err := se.srv.msgs.Choices(context.Background(), se.uid); err == nil {
			return cs
		}
	}
	return messages.BuiltinChoices()
}

// findLanguage is the choice with code, or with that name (any case).
func findLanguage(choices []messages.Choice, s string) (messages.Choice, bool) {
	if s == "" {
		return messages.Choice{}, false
	}
	for _, c := range choices {
		if strings.EqualFold(c.Code, s) {
			return c, true
		}
	}
	for _, c := range choices {
		if strings.EqualFold(c.Name, s) {
			return c, true
		}
	}
	return messages.Choice{}, false
}

func languageLabel(c messages.Choice) string { return c.Name + " (" + c.Code + ")" }

// languageList is "de Deutsch, en English, …".
func languageList(choices []messages.Choice) string {
	parts := make([]string, len(choices))
	for i, c := range choices {
		parts[i] = c.Code + " " + c.Name
	}
	return strings.Join(parts, ", ")
}
