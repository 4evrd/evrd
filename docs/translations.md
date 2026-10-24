# Translating evrd

evrd comes in English, German, Indonesian, Italian, Japanese, Spanish and
Turkish: the web app, and what evrd itself says — to IRC clients, and in
errors, connection logs and events on the website (see [evrd's own
messages](#evrds-own-messages)). Anyone can add a language or change some of the
text, without touching the code:

- **Admins**, in *Admin → Settings → Languages*, upload a translation for
  everyone. It can be a new language (`de`) or changes to a built-in one
  (`it`). It's kept in the database, so every node of a cluster has it.
- **Every user**, in *Account → Profile → Your own translation*, can upload
  one translation for themselves only. It shows up in their *Language* menu.

## Which text wins

For each piece of text, the app uses the first of these that has it:

1. your own translation, when its language is the one you picked;
2. the admin's translation for that language;
3. the built-in language;
4. English.

So a translation doesn't have to be complete: anything it leaves out falls
back. To fix one string of the Italian app for yourself, upload a file for
`it` with only that string.

## Start from a template

*Download template* gives every English text in the format you choose, with
an empty translation where the format has a place for one. *Download* next
to a language gives that language as it is now (built-in text plus the
admin's changes), and *Download yours* gives your own file, so you can edit
an existing translation rather than start over.

| Format | Extension | Notes |
|---|---|---|
| JSON | `.json` | evrd's own: `{"code": "de", "name": "Deutsch", "messages": {…}}`. A flat `{"key": "text"}` object or nested (i18next-style) objects work too. |
| CSV | `.csv` | Columns `key,english,translation` and an optional `notes`. Opens in any spreadsheet. |
| gettext PO | `.po` (template `.po` or `.pot`) | For Poedit and other PO editors. `msgctxt` is the key, `msgid` the English, `msgstr` the translation. |
| XLIFF 1.2 / 2.0 | `.xlf`, `.xliff` | For CAT tools. The unit's `id` is the key, `source` the English, `target` the translation. |
| YAML | `.yaml`, `.yml` | A flat or nested map of `key: "text"`. Only maps of text: no lists, anchors or block (`|`, `>`) text. |

The file is converted to evrd's JSON in your browser before it's uploaded.
Before saving, the upload shows what's in it:

- **errors** that stop the upload: a bad language code or name, a bad key,
  a text over 2000 characters, a plural without `other`, a file over 512 KiB,
  or placeholders that differ from English;
- keys English doesn't have (a warning: they're ignored);
- how many texts are still untranslated, and how many fuzzy PO entries were
  skipped.

When the file doesn't name its language (CSV, a flat JSON file…), the upload
asks for the **code** (`de`, or `pt-BR` for a region; 2 or 3 lowercase
letters) and the **name** in the language itself (`Deutsch`). Using a
built-in language's code changes that language.

## Placeholders

Words in braces, like `{count}`, `{name}` or `{time}`, are filled in by the
app. Keep each one exactly as it is (you can move it within the sentence):
a translation whose placeholders differ from English isn't accepted.

Text is always shown as plain text: HTML in a translation shows as written,
it isn't interpreted.

## evrd's own messages

What evrd itself says is in the same file, as the keys starting with
`irc.`. The template and *Download* include them; a file can have only
those, only the web app's, or both. Admin → Settings → Languages and your
own translation say how many of them a language has. They are:

| Keys | What |
|---|---|
| `irc.help.*`, `irc.usage.*` and most others | `*status`'s replies, its `help`, the welcome lines and PART reasons IRC clients see |
| `irc.err.*`, `irc.web.*` | errors: a network's settings, passwords, bind hosts, modules, language packs… — over IRC and on the website |
| `irc.log.*`, `irc.reason.*`, `irc.rest.*`, `irc.hint.*`, `irc.module.log.*` | a network's connection log, why a connection failed or dropped (timed out, refused, no such host, unreachable, reset, TLS and certificate trouble…), and what to do about it |
| `irc.event.*`, `irc.admin.*`, `irc.ago.*` | the activity feed and notifications, and the admin overview's “needs attention” |
| `irc.module*`, `irc.setting*` | the modules' names, descriptions and settings, and Admin → Settings' groups, labels and help |

Over IRC, evrd answers in the language picked under *Account → Profile →
Language*, with the same order as above: your own translation (for that
language), the admin's, the built-in one, English. With *the browser's
language* picked, evrd can't tell which that is, so IRC replies are in
English. On the website these messages follow the language the app shows,
browser's language included. A change shows on the next command or page
load; a translation an admin uploads reaches every node of a cluster
within 30 seconds.

The connection log and the activity feed keep what evrd said, not the
sentence, so they read in each viewer's language — an admin helping a user
sees the user's log in their own. Events from before evrd kept them that
way stay in English. What IRC servers and services say (their `ERROR`s and
notices, a refused login's reason) is shown as they sent it; only evrd's
words around it are translated, and a failure evrd doesn't recognise is
passed on as it is.

Command names, settings and their values (`connect`, `set nick …`, `on`,
`off`, `auth sasl`) stay as they are in every language, as do
`<arguments>` in usage lines' commands — translate the words around them.
Lines before an IRC client has signed in, IRC protocol replies (numerics,
`FAIL`, `BOUNCER` errors), what evrd logs for admins on the server, and the
errors of evrd.conf settings and of the network directory's file are in
English. IRC clients get `…`, `—`, `·` and curly quotes as `...`, `-` and
straight quotes (some show them as garbage otherwise); letters in any
script pass through as written.

## Plurals

Some texts change with a number (`1 minute ago`, `5 minutes ago`). They have
a form for each of the language's plural categories, as in
[CLDR](https://cldr.unicode.org/index/cldr-spec/plural-rules): `zero`, `one`,
`two`, `few`, `many` and `other`. `other` is required; a missing form uses
`other`.

- **JSON, YAML:** an object of forms, `{"one": "{count} file", "other": "{count} files"}`
  (i18next's `key_one`, `key_other` work too).
- **CSV, XLIFF:** one row or unit per form, with the form after `#` in the
  key: `format.minutesAgo#one`, `format.minutesAgo#other`. Add
  `#few`, `#many`… rows when your language has them.
- **PO:** `msgid_plural` and `msgstr[0]`, `msgstr[1]`…, in the order the
  file's `Plural-Forms` header gives. evrd maps them to categories with the
  usual gettext rules for the language (and otherwise Intl's order: zero,
  one, two, few, many, other). If `nplurals` doesn't match the language, the
  upload says so instead of guessing: fix the header (in Poedit,
  *Translation → Properties*).

## With Poedit

1. Download the template as **PO (Poedit)** (`evrd-template.po`) and open it
   in Poedit. Set your language (*Translation → Properties*, or when Poedit
   asks); Poedit fills in `Plural-Forms` from it.
2. Translate, save, and upload the `.po` file.

Teams that keep one template and a `.po` per language can download the
**POT (gettext template)** instead (`evrd-template.pot`) and use *File → New
from POT/PO File…*.

Entries marked **fuzzy** (*Needs work*) are skipped, so check them before
uploading. Obsolete (`#~`) entries are ignored. The language comes from the
file's `Language` header (`pt_BR` becomes `pt-BR`).

To work on an existing language, download it as PO instead and open that.

## The API

The upload is JSON in evrd's format (above):

- `GET /api/languages`: the admins' languages (`code`, `name`, `updatedAt`).
  Public: the sign-in page uses it.
- `GET /api/languages/{code}`: one of them, with its `messages`.
- `PUT /api/admin/languages/{code}`, `DELETE …`: admins only.
- `GET`, `PUT`, `DELETE /api/me/language-pack`: your own (`GET` is a 404
  when there's none).
- `GET /api/irc-messages/{code}`: a built-in language's own messages of
  evrd's (`irc.*`; `en` has every key). Public.
- `GET /api/irc-messages`: how many of them English has (`total`), and how
  many each language has, built in or from the admin's translation
  (`languages`). Public.

Every request may say which language to answer in with an
`X-Evrd-Language: it` header (`?lang=it` on the chat's WebSocket): the web
app sends the one it shows. Without it, errors, logs and events are in the
account's language.

Invalid files get a `422` with the reason. A user's own translation is
deleted with their account.
