package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// An account can keep any number of public lists, each a named selection of
// its items, readable by anyone holding the list's link at /s/{token}. Items
// are private until put on a list. The token is 128 random bits and the only
// thing in the URL, so a link says nothing about who made it and cannot be
// guessed or walked. Replacing a token kills the old link; clearing it takes
// the list offline but keeps what is on it.

// List is a public list, with how many items are on it.
type List struct {
	ID          int64
	Title       string
	Description string // Markdown, as written
	Token       string // empty when the list has no link
	Count     int
	CreatedAt time.Time
}

// ListRef is the little an item needs to know about a list it is on.
type ListRef struct {
	ID    int64
	Title string
}

// maxListTitle and maxListDescription bound what a list's settings hold, in
// characters.
const (
	maxListTitle       = 100
	maxListDescription = 5000
)

// DescriptionHTML is the description rendered from Markdown, safe to show.
func (l List) DescriptionHTML() template.HTML { return renderMarkdown(l.Description) }

// Lists returns the owner's lists, alphabetically, with their item counts.
func (s *Store) Lists(userID int64) ([]List, error) {
	rows, err := s.db.Query(`SELECT l.id, l.title, l.description, COALESCE(l.token, ''), l.created_at,
			COUNT(li.item_id)
		FROM lists l LEFT JOIN list_items li ON li.list_id = l.id
		WHERE l.user_id = ?
		GROUP BY l.id ORDER BY l.title COLLATE NOCASE, l.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []List
	for rows.Next() {
		var (
			l         List
			createdAt int64
		)
		if err := rows.Scan(&l.ID, &l.Title, &l.Description, &l.Token, &createdAt, &l.Count); err != nil {
			return nil, err
		}
		l.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, l)
	}
	return out, rows.Err()
}

// CreateList adds a list for the owner, with a link ready to hand out.
func (s *Store) CreateList(userID int64, title string) (int64, error) {
	token, err := newListToken()
	if err != nil {
		return 0, err
	}
	res, err := s.db.Exec(`INSERT INTO lists (user_id, title, token, created_at) VALUES (?, ?, ?, ?)`,
		userID, title, token, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateList changes a list's title and description.
func (s *Store) UpdateList(userID, id int64, title, description string) error {
	return s.touch(`UPDATE lists SET title = ?, description = ? WHERE id = ? AND user_id = ?`,
		title, description, id, userID)
}

// NewListLink gives a list a fresh token, which kills any link it had before.
func (s *Store) NewListLink(userID, id int64) error {
	token, err := newListToken()
	if err != nil {
		return err
	}
	return s.touch(`UPDATE lists SET token = ? WHERE id = ? AND user_id = ?`, token, id, userID)
}

// ClearListLink takes a list offline, keeping its items.
func (s *Store) ClearListLink(userID, id int64) error {
	return s.touch(`UPDATE lists SET token = NULL WHERE id = ? AND user_id = ?`, id, userID)
}

// DeleteList removes a list. The items on it are untouched.
func (s *Store) DeleteList(userID, id int64) error {
	return s.touch(`DELETE FROM lists WHERE id = ? AND user_id = ?`, id, userID)
}

// ToggleListItem puts one of the owner's items on one of their lists, or takes
// it off. Both must belong to the owner.
func (s *Store) ToggleListItem(userID, itemID, listID int64) error {
	return s.tx(func(tx *sql.Tx) error {
		var mine int
		err := tx.QueryRow(`SELECT COUNT(*) FROM items i, lists l
			WHERE i.id = ? AND i.user_id = ? AND l.id = ? AND l.user_id = ?`,
			itemID, userID, listID, userID).Scan(&mine)
		if err != nil {
			return err
		}
		if mine == 0 {
			return ErrNotFound
		}
		res, err := tx.Exec(`DELETE FROM list_items WHERE list_id = ? AND item_id = ?`, listID, itemID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			return nil
		}
		_, err = tx.Exec(`INSERT INTO list_items (list_id, item_id) VALUES (?, ?)`, listID, itemID)
		return err
	})
}

// ListByToken resolves a public link to its list and the account that owns it.
func (s *Store) ListByToken(token string) (int64, List, error) {
	var (
		userID int64
		l      List
	)
	if token == "" {
		return 0, l, ErrNotFound
	}
	err := s.db.QueryRow(`SELECT user_id, id, title, description FROM lists WHERE token = ?`, token).
		Scan(&userID, &l.ID, &l.Title, &l.Description)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, l, ErrNotFound
	}
	return userID, l, err
}

// attachLists fills in the Lists field for every item in one extra query.
func (s *Store) attachLists(items []Item) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]any, 0, len(items))
	byID := make(map[int64]int, len(items))
	for i, item := range items {
		ids = append(ids, item.ID)
		byID[item.ID] = i
	}
	query := fmt.Sprintf(`SELECT li.item_id, l.id, l.title FROM list_items li
		JOIN lists l ON l.id = li.list_id
		WHERE li.item_id IN (%s)
		ORDER BY l.title COLLATE NOCASE, l.id`, placeholders(len(ids)))

	rows, err := s.db.Query(query, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			itemID int64
			ref    ListRef
		)
		if err := rows.Scan(&itemID, &ref.ID, &ref.Title); err != nil {
			return err
		}
		if i, ok := byID[itemID]; ok {
			items[i].Lists = append(items[i].Lists, ref)
		}
	}
	return rows.Err()
}

func newListToken() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// cleanListTitle trims a submitted title and checks it is usable.
func cleanListTitle(raw string) (string, error) {
	title := strings.Join(strings.Fields(raw), " ")
	if title == "" {
		return "", errors.New("give the list a title")
	}
	if utf8.RuneCountInString(title) > maxListTitle {
		return "", fmt.Errorf("keep the title under %d characters", maxListTitle)
	}
	return title, nil
}

// cleanListDescription trims a submitted description and checks its length.
// Browsers send textarea line breaks as CRLF; store plain LF.
func cleanListDescription(raw string) (string, error) {
	desc := strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	if n := utf8.RuneCountInString(desc); n > maxListDescription {
		return "", fmt.Errorf("keep the description under %d characters (it has %d)", maxListDescription, n)
	}
	return desc, nil
}

// publicItem is everything a visitor to a public list may see about an item.
// It is a separate type, not an Item, so notes, status, stars, tags and the
// owner's other lists cannot reach the public page by a template slip.
type publicItem struct {
	Title   string
	URL     string
	Kind    string
	Creator string
}

func (p publicItem) KindLabel() string { return kindBySlug(p.Kind).Label }
func (p publicItem) Host() string      { return hostOf(p.URL) }

// publicData is the dot for the public list template.
type publicData struct {
	Title       string
	Description template.HTML
	Items       []publicItem
}

// handlePublicList shows a list to anyone holding its link.
func (a *app) handlePublicList(w http.ResponseWriter, r *http.Request) {
	userID, list, err := a.store.ListByToken(r.PathValue("token"))
	if errors.Is(err, ErrNotFound) {
		http.Error(w, "This list does not exist, or is no longer shared.", http.StatusNotFound)
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}
	items, err := a.store.List(userID, Filter{List: list.ID, Sort: DefaultSort})
	if err != nil {
		a.serverError(w, err)
		return
	}
	data := publicData{
		Title:       list.Title,
		Description: list.DescriptionHTML(),
		Items:       make([]publicItem, 0, len(items)),
	}
	for _, it := range items {
		data.Items = append(data.Items, publicItem{
			Title: it.Title, URL: it.URL, Kind: it.Kind, Creator: it.Creator,
		})
	}

	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	// The token is the key to the list: keep it out of the Referer sent to the
	// sites it links to, out of search engines, and out of shared caches.
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("Cache-Control", "no-store")
	// The page runs no script at all, so forbid it outright: even if something
	// unsafe ever slipped through the description, it could not execute.
	h.Set("Content-Security-Policy", publicCSP)
	if err := a.tmpl.ExecuteTemplate(w, "public.html", data); err != nil {
		log.Printf("render public: %v", err)
	}
}

// listView is a list as the lists page shows it, with its full link.
type listView struct {
	List
	ShareURL string
}

// listsData is the dot for the lists page.
type listsData struct {
	Title string
	User  User
	Lists []listView
	Error string
}

// handleLists shows the owner's public lists and their links.
func (a *app) handleLists(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	lists, err := a.store.Lists(user.ID)
	if err != nil {
		a.serverError(w, err)
		return
	}
	data := listsData{Title: "Public lists", User: user, Error: r.URL.Query().Get("error")}
	for _, l := range lists {
		v := listView{List: l}
		if l.Token != "" {
			v.ShareURL = shareURL(r, l.Token)
		}
		data.Lists = append(data.Lists, v)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.tmpl.ExecuteTemplate(w, "lists.html", data); err != nil {
		log.Printf("render lists: %v", err)
	}
}

// handleCreateList makes a new list, link and all.
func (a *app) handleCreateList(w http.ResponseWriter, r *http.Request) {
	title, err := cleanListTitle(r.FormValue("title"))
	if err != nil {
		http.Redirect(w, r, withError("/lists", err.Error()), http.StatusSeeOther)
		return
	}
	id, err := a.store.CreateList(currentUser(r).ID, title)
	if err != nil {
		a.serverError(w, err)
		return
	}
	http.Redirect(w, r, listAnchor(id), http.StatusSeeOther)
}

// publicCSP allows the page's own stylesheet, images over https (and the
// inline favicon), and nothing else: no script, no frames, no forms.
const publicCSP = "default-src 'none'; style-src 'self'; img-src 'self' https: data:; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// handleUpdateList saves a list's title and description.
func (a *app) handleUpdateList(w http.ResponseWriter, r *http.Request) {
	title, err := cleanListTitle(r.FormValue("title"))
	if err == nil {
		var desc string
		if desc, err = cleanListDescription(r.FormValue("description")); err == nil {
			a.changeList(w, r, func(userID, id int64) error {
				return a.store.UpdateList(userID, id, title, desc)
			})
			return
		}
	}
	http.Redirect(w, r, withError("/lists", err.Error()), http.StatusSeeOther)
}

// handleNewListLink gives a list a new link, or its first one again.
func (a *app) handleNewListLink(w http.ResponseWriter, r *http.Request) {
	a.changeList(w, r, a.store.NewListLink)
}

// handleClearListLink takes a list offline.
func (a *app) handleClearListLink(w http.ResponseWriter, r *http.Request) {
	a.changeList(w, r, a.store.ClearListLink)
}

// handleDeleteList removes a list; its items stay in the catalogue.
func (a *app) handleDeleteList(w http.ResponseWriter, r *http.Request) {
	a.changeList(w, r, a.store.DeleteList)
}

// changeList runs a change to one of the owner's lists and returns to it.
func (a *app) changeList(w http.ResponseWriter, r *http.Request, do func(userID, id int64) error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := do(currentUser(r).ID, id); err != nil {
		a.mutationError(w, r, err)
		return
	}
	http.Redirect(w, r, listAnchor(id), http.StatusSeeOther)
}

// handleToggleListItem puts an item on a list, or takes it off.
func (a *app) handleToggleListItem(w http.ResponseWriter, r *http.Request) {
	listID, err := strconv.ParseInt(r.PathValue("list"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a.mutate(w, r, func(userID, id int64) error { return a.store.ToggleListItem(userID, id, listID) })
}

func listAnchor(id int64) string { return "/lists#list-" + strconv.FormatInt(id, 10) }

// shareURL is the absolute public link for a token, as the browser reached us.
func shareURL(r *http.Request, token string) string {
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/s/" + token
}
