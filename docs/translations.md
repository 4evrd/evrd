# Translating evrd

The web app comes in English, Italian, Spanish and Turkish. Anyone can add a
language or change some of the text, without touching the code:

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
| gettext PO | `.po` (template `.pot`) | For Poedit and other PO editors. `msgctxt` is the key, `msgid` the English, `msgstr` the translation. |
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

1. Download the template as **gettext PO** (`evrd-template.pot`).
2. In Poedit, *File → New from POT/PO File…*, pick the template and your
   language. Poedit fills in `Language` and `Plural-Forms`.
3. Translate, save, and upload the `.po` file.

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

Invalid files get a `422` with the reason. A user's own translation is
deleted with their account.
