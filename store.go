package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when an item id does not exist.
var ErrNotFound = errors.New("item not found")

// schemaVersion is the migration level this build expects. It is tracked in
// the database's own user_version pragma.
const schemaVersion = 5

const schema = `
CREATE TABLE IF NOT EXISTS items (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id    INTEGER NOT NULL DEFAULT 0,
	kind       TEXT NOT NULL DEFAULT 'article',
	url        TEXT NOT NULL DEFAULT '',
	title      TEXT NOT NULL,
	creator    TEXT NOT NULL DEFAULT '',
	notes      TEXT NOT NULL DEFAULT '',
	status     TEXT NOT NULL DEFAULT 'queued',
	favorite   INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS tags (
	id   INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL COLLATE NOCASE UNIQUE
);

CREATE TABLE IF NOT EXISTS item_tags (
	item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
	tag_id  INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
	PRIMARY KEY (item_id, tag_id)
);

CREATE TABLE IF NOT EXISTS users (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	email         TEXT NOT NULL COLLATE NOCASE UNIQUE,
	password_hash TEXT NOT NULL,
	created_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
	token_hash TEXT PRIMARY KEY,
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL
);

-- Public lists. The token is the whole of the public URL, so it is random and
-- says nothing about whose list it is; NULL means the list has no link.
CREATE TABLE IF NOT EXISTS lists (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	title       TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	token       TEXT UNIQUE,
	created_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS list_items (
	list_id INTEGER NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
	item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
	PRIMARY KEY (list_id, item_id)
);

CREATE INDEX IF NOT EXISTS idx_item_tags_tag ON item_tags(tag_id);
CREATE INDEX IF NOT EXISTS idx_items_created ON items(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_items_kind ON items(kind);
CREATE INDEX IF NOT EXISTS idx_items_user ON items(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_lists_user ON lists(user_id);
CREATE INDEX IF NOT EXISTS idx_list_items_item ON list_items(item_id);
`

// upgradeV2 adds accounts to a catalogue that had none. Existing items are left
// with user_id 0 — "unowned" — and the first account created adopts them, so an
// upgrade never silently hides someone's catalogue behind a login they lack.
const upgradeV2 = `
ALTER TABLE items ADD COLUMN user_id INTEGER NOT NULL DEFAULT 0;
`

// upgradeV5 gives lists a description, shown on the public page. It runs
// before the schema, against the lists table v4 created.
const upgradeV5 = `
ALTER TABLE lists ADD COLUMN description TEXT NOT NULL DEFAULT '';
`

// upgradeV4 replaces v3's single public list per account — a public flag on
// items plus one share_links token — with named lists. Anyone who had shared
// something gets a list holding exactly that, under the same link, so nothing
// already handed out stops working. It runs after the schema, which creates
// the lists tables it fills. A v2 database never had either, and skips it.
const upgradeV4 = `
INSERT INTO lists (user_id, title, token, created_at)
	SELECT u.id, 'Shared list', s.token, COALESCE(s.created_at, CAST(strftime('%s', 'now') AS INTEGER))
	FROM users u LEFT JOIN share_links s ON s.user_id = u.id
	WHERE s.token IS NOT NULL
		OR EXISTS (SELECT 1 FROM items i WHERE i.user_id = u.id AND i.public = 1);

INSERT INTO list_items (list_id, item_id)
	SELECT l.id, i.id FROM items i JOIN lists l ON l.user_id = i.user_id
	WHERE i.public = 1;

DROP TABLE share_links;
ALTER TABLE items DROP COLUMN public;
`

// legacyUpgrade turns the original articles-only schema into the catalogue.
// Renaming a table carries its rows, indexes and foreign key references, so
// nothing is copied and nothing is lost.
const legacyUpgrade = `
ALTER TABLE articles ADD COLUMN kind TEXT NOT NULL DEFAULT 'article';
ALTER TABLE articles ADD COLUMN creator TEXT NOT NULL DEFAULT '';
ALTER TABLE articles RENAME TO items;
ALTER TABLE article_tags RENAME TO item_tags;
ALTER TABLE item_tags RENAME COLUMN article_id TO item_id;

UPDATE items SET status = CASE status
	WHEN 'unread'  THEN 'queued'
	WHEN 'reading' THEN 'active'
	WHEN 'read'    THEN 'done'
	ELSE 'queued'
END;

DROP INDEX IF EXISTS idx_article_tags_tag;
DROP INDEX IF EXISTS idx_articles_created;
`

// Store is the SQLite-backed persistence layer.
type Store struct {
	db *sql.DB
}

// Open connects to the SQLite file at path and brings its schema up to date.
func Open(path string) (*Store, error) {
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// The pure-Go driver is happiest with a single writer.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// migrate applies whatever steps this database is missing.
func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version >= schemaVersion {
		return nil
	}

	var steps string

	// A database with a version stamp, or the original articles table, already
	// has items and needs columns added to it; an empty one gets them from the
	// schema below and must skip the ALTERs.
	existing := version >= 1
	if version < 1 {
		legacy, err := tableExists(db, "articles")
		if err != nil {
			return err
		}
		if legacy {
			steps += legacyUpgrade
			existing = true
		}
	}
	if existing && version < 2 {
		steps += upgradeV2
	}
	if version == 4 {
		steps += upgradeV5
	}
	steps += schema
	if version == 3 {
		steps += upgradeV4
	}
	steps += fmt.Sprintf("PRAGMA user_version = %d;", schemaVersion)

	// One transaction, version stamp included, so a step that fails leaves the
	// database exactly as it was rather than half upgraded.
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(steps); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func tableExists(db *sql.DB, name string) (bool, error) {
	var found string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

const itemColumns = `i.id, i.kind, i.url, i.title, i.creator, i.notes, i.status,
	i.favorite, i.created_at`

// Every query below is scoped to one owner. An item belongs to exactly one
// account, and nothing in the app ever reads across that line.

// orderBy maps a sort slug to its ORDER BY clause. Every clause here is a
// literal written by hand, never input, and knows this file's table alias.
func orderBy(slug string) string {
	switch sortBySlug(slug).Slug {
	case "updated":
		return "i.updated_at DESC, i.id DESC"
	case "title":
		return "i.title COLLATE NOCASE ASC, i.id DESC"
	case "kind":
		return "i.kind ASC, i.created_at DESC, i.id DESC"
	default:
		return "i.created_at DESC, i.id DESC"
	}
}

// List returns the owner's items matching f, in the order f asks for.
func (s *Store) List(userID int64, f Filter) ([]Item, error) {
	where := []string{"i.user_id = ?"}
	args := []any{userID}
	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + q + "%"
		where = append(where, "(i.title LIKE ? OR i.creator LIKE ? OR i.url LIKE ? OR i.notes LIKE ?)")
		args = append(args, like, like, like, like)
	}
	if f.Tag != "" {
		where = append(where, `i.id IN (
			SELECT it.item_id FROM item_tags it
			JOIN tags t ON t.id = it.tag_id
			WHERE t.name = ? COLLATE NOCASE)`)
		args = append(args, f.Tag)
	}
	if f.Kind != "" {
		where = append(where, "i.kind = ?")
		args = append(args, f.Kind)
	}
	if f.Status != "" {
		where = append(where, "i.status = ?")
		args = append(args, f.Status)
	}
	if f.Favorite {
		where = append(where, "i.favorite = 1")
	}
	if f.List != 0 {
		where = append(where, "i.id IN (SELECT item_id FROM list_items WHERE list_id = ?)")
		args = append(args, f.List)
	}

	// where always holds the owner clause, so the WHERE is never optional.
	query := `SELECT ` + itemColumns + ` FROM items i` +
		" WHERE " + strings.Join(where, " AND ") +
		" ORDER BY " + orderBy(f.Sort)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Item
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, s.attach(items)
}

// attach fills in what each item carries beyond its own row.
func (s *Store) attach(items []Item) error {
	if err := s.attachTags(items); err != nil {
		return err
	}
	return s.attachLists(items)
}

// attachTags fills in the Tags field for every item in one extra query.
func (s *Store) attachTags(items []Item) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]any, 0, len(items))
	byID := make(map[int64]int, len(items))
	for i, item := range items {
		ids = append(ids, item.ID)
		byID[item.ID] = i
	}
	query := fmt.Sprintf(`SELECT it.item_id, t.name FROM item_tags it
		JOIN tags t ON t.id = it.tag_id
		WHERE it.item_id IN (%s)
		ORDER BY t.name COLLATE NOCASE`, placeholders(len(ids)))

	rows, err := s.db.Query(query, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id   int64
			name string
		)
		if err := rows.Scan(&id, &name); err != nil {
			return err
		}
		if i, ok := byID[id]; ok {
			items[i].Tags = append(items[i].Tags, name)
		}
	}
	return rows.Err()
}

// Get returns one of the owner's items with its tags.
func (s *Store) Get(userID, id int64) (Item, error) {
	row := s.db.QueryRow(`SELECT `+itemColumns+` FROM items i
		WHERE i.id = ? AND i.user_id = ?`, id, userID)
	item, err := scanItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, err
	}
	one := []Item{item}
	if err := s.attach(one); err != nil {
		return Item{}, err
	}
	return one[0], nil
}

// Exists reports whether the owner has this item, without loading it.
func (s *Store) Exists(userID, id int64) error {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM items WHERE id = ? AND user_id = ?`, id, userID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// Create inserts a new item and returns its id. Kind and status are expected
// to be valid already; the handlers normalize them at the form boundary.
func (s *Store) Create(userID int64, item Item) (int64, error) {
	now := time.Now().Unix()
	var id int64
	err := s.tx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT INTO items
			(user_id, kind, url, title, creator, notes, status, favorite, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			userID, item.Kind, item.URL, item.Title, item.Creator, item.Notes,
			item.Status, 0, now, now)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return err
		}
		return setTags(tx, id, item.Tags)
	})
	return id, err
}

// Update saves the editable fields of an item along with its tags.
func (s *Store) Update(userID int64, item Item) error {
	return s.tx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE items
			SET kind = ?, url = ?, title = ?, creator = ?, notes = ?, status = ?, updated_at = ?
			WHERE id = ? AND user_id = ?`,
			item.Kind, item.URL, item.Title, item.Creator, item.Notes,
			item.Status, time.Now().Unix(), item.ID, userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return setTags(tx, item.ID, item.Tags)
	})
}

// SetStatus moves an item along the queue.
func (s *Store) SetStatus(userID, id int64, status string) error {
	return s.touch(`UPDATE items SET status = ?, updated_at = ? WHERE id = ? AND user_id = ?`,
		status, time.Now().Unix(), id, userID)
}

// ToggleFavorite flips the star on an item.
func (s *Store) ToggleFavorite(userID, id int64) error {
	return s.touch(`UPDATE items SET favorite = 1 - favorite, updated_at = ? WHERE id = ? AND user_id = ?`,
		time.Now().Unix(), id, userID)
}

// touch runs an update that must match exactly one item.
func (s *Store) touch(query string, args ...any) error {
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes an item and its tag links.
func (s *Store) Delete(userID, id int64) error {
	return s.tx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM items WHERE id = ? AND user_id = ?`, id, userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return pruneTags(tx)
	})
}

// Tags returns the owner's tags with their item counts, most used first. Tag
// names are shared storage, but a count only ever covers one owner's items.
func (s *Store) Tags(userID int64) ([]TagCount, error) {
	rows, err := s.db.Query(`SELECT t.name, COUNT(it.item_id) AS n
		FROM tags t
		JOIN item_tags it ON it.tag_id = t.id
		JOIN items i ON i.id = it.item_id
		WHERE i.user_id = ?
		GROUP BY t.id ORDER BY n DESC, t.name COLLATE NOCASE`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TagCount
	for rows.Next() {
		var tc TagCount
		if err := rows.Scan(&tc.Name, &tc.Count); err != nil {
			return nil, err
		}
		out = append(out, tc)
	}
	return out, rows.Err()
}

// Counts is the tally behind the sidebar. Statuses and kinds are kept in
// separate maps so the two vocabularies can never overwrite each other.
type Counts struct {
	Statuses  map[string]int
	Kinds     map[string]int
	Total     int
	Favorites int
}

// Counts tallies the owner's catalogue in a single grouped scan.
func (s *Store) Counts(userID int64) (Counts, error) {
	counts := Counts{Statuses: map[string]int{}, Kinds: map[string]int{}}

	rows, err := s.db.Query(`SELECT status, kind, COUNT(*), SUM(favorite)
		FROM items WHERE user_id = ? GROUP BY status, kind`, userID)
	if err != nil {
		return counts, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			status, kind string
			n, favorites int
		)
		if err := rows.Scan(&status, &kind, &n, &favorites); err != nil {
			return counts, err
		}
		counts.Statuses[status] += n
		counts.Kinds[kind] += n
		counts.Total += n
		counts.Favorites += favorites
	}
	return counts, rows.Err()
}

// setTags replaces an item's tags, creating any tag rows it needs.
func setTags(tx *sql.Tx, itemID int64, tags []string) error {
	if _, err := tx.Exec(`DELETE FROM item_tags WHERE item_id = ?`, itemID); err != nil {
		return err
	}
	for _, name := range tags {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO tags (name) VALUES (?)`, name); err != nil {
			return err
		}
		var tagID int64
		if err := tx.QueryRow(`SELECT id FROM tags WHERE name = ? COLLATE NOCASE`, name).Scan(&tagID); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO item_tags (item_id, tag_id) VALUES (?, ?)`,
			itemID, tagID); err != nil {
			return err
		}
	}
	return pruneTags(tx)
}

// pruneTags drops tags that no item uses any more.
func pruneTags(tx *sql.Tx) error {
	_, err := tx.Exec(`DELETE FROM tags WHERE id NOT IN (SELECT tag_id FROM item_tags)`)
	return err
}

// tx runs fn inside a transaction, rolling back on error.
func (s *Store) tx(fn func(*sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// scanner covers both *sql.Row and *sql.Rows.
type scanner interface{ Scan(dest ...any) error }

func scanItem(sc scanner) (Item, error) {
	var (
		item      Item
		createdAt int64
	)
	err := sc.Scan(&item.ID, &item.Kind, &item.URL, &item.Title, &item.Creator,
		&item.Notes, &item.Status, &item.Favorite, &createdAt)
	if err != nil {
		return Item{}, err
	}
	item.CreatedAt = time.Unix(createdAt, 0)
	return item, nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
