# Catalogue

A small Go web app for curating things you want to get to: articles, videos,
books, films, documentaries, podcasts, websites. Save it, categorise it, tag it,
and come back later to write notes about it.

Server and UI are both Go — `net/http` for routing, `html/template` for the
pages, SQLite for storage. No JavaScript build step, no frontend framework.

## Running it

```sh
go build -o catalogue .
./catalogue -add-user you@example.com   # create your account, then:
./catalogue                             # http://localhost:8080, db in ./reading-list.db
```

Templates, CSS and script are embedded, so the binary is self-contained. Flags:
`-addr` to change the listen address, `-db` to point at another database file.

## Running it in Docker

```sh
docker compose up -d --build
docker compose run --rm catalogue -add-user you@example.com
curl -si 127.0.0.1:8080 | head -1   # expect 303, to /login
```

The database is `sqlite/reading-list.db` on the host, mounted at `/data` in the
container. It is not in the image, so rebuilding or removing the container
leaves it alone. The whole folder is mounted rather than the one file because
WAL mode keeps `-wal` and `-shm` files beside the database.

Anything after the service name is passed to the app as flags, so the account
commands below all work this way, with the app running:

```sh
docker compose run --rm catalogue -list-users
docker compose run --rm catalogue -passwd you@example.com
```

The port is published on loopback only, so the Caddy block below works
unchanged. `restart: unless-stopped` brings the app back after a crash or a
reboot, as the systemd unit did.

**Permissions.** The container runs as UID 1000, which has to own `sqlite/`. If
the app exits with `unable to open database file (14)`, it does not. Compare
`ls -ldn sqlite` with `id`, and if your account is not 1000, tell Compose:

```sh
printf 'CATALOGUE_UID=%s\nCATALOGUE_GID=%s\n' "$(id -u)" "$(id -g)" > .env
```

**Moving from the systemd service.** Run one or the other, never both: they
would compete for the port and the database. Move the `-wal` along with the
`.db`, since a version without clean shutdown may have left data in it.

```sh
sudo systemctl disable --now catalogue
mv reading-list.db* sqlite/
docker compose up -d --build
```

**Deploying a new version.** `git pull && docker compose up -d --build` on the
server. Compiling SQLite is heavy for a small VPS, so you can also build on your
laptop and copy the image over:

```sh
docker buildx build --platform linux/amd64 -t catalogue --load .
docker save catalogue | ssh nitesh@72.62.31.251 docker load
ssh nitesh@72.62.31.251 'cd ~/reading-list-manager && docker compose up -d --no-build'
```

**Backups.** Stopping the container checkpoints the WAL away, which leaves the
`.db` self-contained:

```sh
docker compose stop
cp sqlite/reading-list.db ~/backups/catalogue-$(date +%F).db
docker compose start
```

**On a Mac, one process at a time.** Docker Desktop's file sharing does not
reliably carry SQLite's locks between processes, so a second process can decide
it is alone and delete the `-wal` file from under the running app. Anything
written after that is lost if the app does not then stop cleanly, and nothing
reports an error. Stop the app before an account command, and do not open the
database from the host while the container is up:

```sh
docker compose stop
docker compose run --rm catalogue -add-user you@example.com
docker compose start
```

A Linux host has none of this: containers share its kernel and its locks.

## Accounts

Every item belongs to exactly one account. You only ever see your own items,
tags and counts — a second account gets an empty catalogue, not a shared one.

You sign in with an email address and a password. The email is only an
identifier — nothing is ever sent to it.

**There is no sign-up page, by design.** Accounts are created from the command
line, against the database file:

```sh
./catalogue -add-user you@example.com   # prompts for a password, twice, without echo
./catalogue -list-users
./catalogue -passwd you@example.com     # reset: also signs that account out everywhere
```

The password is read from the terminal with echo off, so it never lands in your
shell history or on screen. If stdin is not a terminal it is read as two plain
lines, which lets you script it.

Once signed in, **the account page** (click your email in the header) changes
your own password. That one asks for the current password first, and signs out
every *other* device while keeping the one you are using.

The **first** account created also adopts any items that predate accounts, so
upgrading a single-user catalogue keeps everything you had.

Passwords are stored as bcrypt hashes. A login issues a random 256-bit session
token; only a SHA-256 of it is stored, so the database never holds a usable key.
The cookie is `HttpOnly`, `SameSite=Lax`, and `Secure` whenever the request
arrived over TLS — including when a reverse proxy terminated it and said so via
`X-Forwarded-Proto`. Sessions last 30 days.

## Putting it behind Caddy

```
catalogue.example.com {
    reverse_proxy localhost:8080
}
```

Run the app bound to localhost so it is only reachable through Caddy:

```sh
./catalogue -addr localhost:8080 -db /var/lib/catalogue/catalogue.db
```

Caddy handles the certificate and terminates TLS; the app sees
`X-Forwarded-Proto: https` and marks the session cookie `Secure` accordingly.

## What it does

- **Save anything.** A link is optional — books and films rarely have one worth
  keeping. An item needs a title *or* a link; give a link and leave the title
  blank and the app fetches the page's `<title>` for you.
- **Two ways of organising, deliberately kept apart.**
  - **Category** — *what the thing is*. One per item, from a fixed set: article,
    video, book, film, documentary, podcast, website, other. Because the set is
    closed, the sidebar counts, the category filter and "sort by category" are
    all reliable. Each category gets a quiet colour on the left edge of its row.
  - **Tags** — *what it's about*. Many per item, free-form, case-insensitively
    de-duplicated. Tags no item uses any more are pruned automatically.
- **Creator, labelled in context.** One field that the form calls *Author* for a
  book, *Director* for a film, *Channel* for a video, *Site* for an article. It
  shows in the row and is searchable.
- **Track the queue.** Every item is *Queued*, *In progress* or *Done* —
  medium-neutral, because a film is not "read". Changeable straight from the
  list. Items can also be starred.
- **Sort and find.** Order by recently added, recently updated, title A–Z or
  category. Search covers titles, creators, links and notes, and combines with
  the category, tag and status filters.

## Public lists

You can keep any number of **public lists**, each a named selection of your
items that anyone with its link can read, no sign-in needed. Make them on the
**Lists** page (linked in the header). Every item starts private. In an item's
*Notes* panel there is one toggle per list, to put it on that list or take it off.
In the main list, small chips show which lists an item is on, and the sidebar
filters by list.

Each list's link looks like `/s/bgivl4nfxuk-eYiNY5I0hg`: 128 random bits and
nothing else, so it says nothing about whose list it is and cannot be guessed.
Per list, *Replace link* issues a new one and the old one stops working.
*Turn off link* takes the list offline but keeps what is on it. Deleting a
list never deletes its items.

Each list can have a **description**, written in Markdown (`**bold**`,
`*italic*`, `[links](https://…)`, lists, `## headings`, `> quotes`), which shows
under the title on the public page. The Lists page has a preview. Raw HTML is
not allowed: one account's writing is shown to anyone with the link, so
`goldmark` runs in its safe mode, which drops HTML and empties `javascript:`
and `data:` links. As a second line of defence, the public page is served
with a Content-Security-Policy that forbids all script; the page uses none.

The public page is deliberately thin: the list's title and description, then each item's title,
link, category, creator and site. Notes, status, stars, tags, dates, your other
lists and your email never reach it. The handler copies items into a separate
type that has no such fields. The page is sent with `Referrer-Policy:
no-referrer`, so clicking out does not leak the token, plus `noindex` and
`no-store`.

## Writing notes

Clicking *Notes* slides a panel in over the list rather than navigating to a
separate page, so you never lose your place. Escape, the close button or a click
outside closes it — and if you have unsaved edits, closing saves them first.
`Cmd`/`Ctrl` + `Enter` in the notes field saves too.

The panel is a real URL: `/?panel=42` renders the list *and* the open panel
server-side, so it survives a reload, works with the back button, and is
bookmarkable. `/items/42` redirects there, and `/articles/42` still works for
bookmarks made before the catalogue existed.

`static/app.js` is pure enhancement on top of that. It intercepts local links
and form posts, fetches the same URL the browser would have, and swaps the
`#main` and `#panel-host` regions into place — so the panel slides rather than
reloads and your scroll position survives. With JavaScript off, every link and
button is an ordinary link and form post and the app behaves identically, just
with full page loads.

## Layout

| File | What's in it |
| --- | --- |
| `main.go` | Flags, routes, template parsing, server setup |
| `auth.go` | Session cookies, the login gate, sign in and out |
| `users.go` | Accounts, password hashing, session storage |
| `usercmd.go` | The `-add-user` / `-passwd` / `-list-users` commands |
| `handlers.go` | One handler per route, form parsing and validation |
| `lists.go` | Public lists: storage, the lists page and the public page |
| `markdown.go` | Safe Markdown rendering for list descriptions |
| `store.go` | SQLite schema, migrations and every query |
| `models.go` | `Item`, the category / status / sort vocabularies, input cleaning |
| `title.go` | Best-effort `<title>` fetching for pasted links |
| `templates/` | `index.html`, the `panel.html` drawer, `lists.html`, the public `public.html`, `login.html`, shared partials |
| `static/style.css` | All the styling, with a dark mode |
| `static/app.js` | Progressive enhancement, ~150 lines |
| `Dockerfile`, `compose.yaml` | The container image, and running it with `sqlite/` mounted |

## Notes on the storage

`modernc.org/sqlite` is a pure-Go SQLite, so there is no cgo and no system
SQLite to install — `go build` produces a binary you can copy anywhere. The
database runs in WAL mode with foreign keys on; `item_tags` is a plain join
table with `ON DELETE CASCADE`, so deleting an item takes its tag links with it.

The schema is versioned in SQLite's own `user_version` pragma and migrated on
startup, one step at a time, so a database at any age catches up on first run.

- **v1** upgrades the original articles-only reading list. The tables are
  *renamed* rather than rebuilt, so rows, indexes and foreign keys carry over
  untouched; `unread`/`reading`/`read` become `queued`/`active`/`done`, and
  everything already saved becomes an *Article*.
- **v2** adds `users` and `sessions`, and an owner column on `items`. Existing
  items are left unowned rather than guessed at, and the first account created
  adopts them — so upgrading a single-user catalogue loses nothing.

- **v3** (briefly) shared one list per account: a `public` flag on items and a
  `share_links` token.
- **v4** replaces that with `lists` and `list_items`, so an account can have
  many lists. A v3 database's shared items move to a list called "Shared list"
  under the same link, so no link already handed out breaks. Each migration now
  runs in one transaction, so a failure leaves the database untouched.
- **v5** adds a Markdown `description` to lists.

Every query against `items` is scoped to one owner — there is no code path that
reads across accounts.

Back up your catalogue by copying the `.db` file. If a `-wal` file exists
beside it at the time, copy that too and keep the pair together — restoring a
`.db` next to a `-wal` from a *different* moment will corrupt it. Stopping the
app first checkpoints the WAL away and leaves the `.db` self-contained, which
is the simplest thing to copy.
