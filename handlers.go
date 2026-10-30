package main

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// pageData is what the list template receives. The item being edited, if any,
// rides along in Panel so the list stays on screen behind it.
type pageData struct {
	Title  string
	User   User
	Items  []Item
	Panel  *Item
	Tags   []TagCount
	Lists  []List
	Counts Counts
	Filter Filter
	Error  string
	// Return is the current list URL, panel and all: where forms come back to.
	Return string
	// ListReturn is the same URL with the panel closed.
	ListReturn string
}

// PanelLink is the URL that opens the given item's panel over this list.
func (p pageData) PanelLink(id int64) string { return panelURL(p.ListReturn, id) }

// panelURL is base with this item's panel opened over it, replacing whichever
// panel was open before.
func panelURL(base string, id int64) string {
	return addParam(dropParams(base, "panel", "error"), "panel", strconv.FormatInt(id, 10))
}

// NewItem is the blank item the add form starts from.
func (p pageData) NewItem() *Item {
	return &Item{Kind: DefaultKind, Status: StatusQueued}
}

// starCtx is the dot for the shared "star" template.
type starCtx struct {
	ID       int64
	Favorite bool
	Return   string
}

// Star wraps an item for the shared favourite-toggle template.
func (p pageData) Star(id int64, favorite bool) starCtx {
	return starCtx{ID: id, Favorite: favorite, Return: p.Return}
}

// listToggleCtx is the dot for the shared "listToggle" template.
type listToggleCtx struct {
	ItemID int64
	List   List
	On     bool
	Return string
}

// ListToggle wraps an item and one of the lists for the on/off toggle.
func (p pageData) ListToggle(item *Item, l List) listToggleCtx {
	return listToggleCtx{ItemID: item.ID, List: l, On: item.InList(l.ID), Return: p.Return}
}

// ListTitle is the title of one of the owner's lists.
func (p pageData) ListTitle(id int64) string {
	for _, l := range p.Lists {
		if l.ID == id {
			return l.Title
		}
	}
	return ""
}

// handleIndex renders the list, filtered and ordered by the query string.
func (a *app) handleIndex(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	q := r.URL.Query()
	f := Filter{
		Query:    strings.TrimSpace(q.Get("q")),
		Tag:      strings.TrimSpace(q.Get("tag")),
		Favorite: q.Get("favorite") == "1",
		Sort:     sortBySlug(q.Get("sort")).Slug,
	}
	f.Status, _ = validStatus(q.Get("status"))
	f.Kind, _ = validKind(q.Get("kind"))

	lists, err := a.store.Lists(user.ID)
	if err != nil {
		a.serverError(w, err)
		return
	}
	// Only one of this account's own lists can narrow the view.
	if id, err := strconv.ParseInt(q.Get("list"), 10, 64); err == nil {
		for _, l := range lists {
			if l.ID == id {
				f.List = id
			}
		}
	}

	items, err := a.store.List(user.ID, f)
	if err != nil {
		a.serverError(w, err)
		return
	}

	panel, err := a.panelItem(user.ID, q.Get("panel"), items)
	if err != nil {
		a.serverError(w, err)
		return
	}
	tags, err := a.store.Tags(user.ID)
	if err != nil {
		a.serverError(w, err)
		return
	}
	counts, err := a.store.Counts(user.ID)
	if err != nil {
		a.serverError(w, err)
		return
	}

	title := "Catalogue"
	if panel != nil {
		title = panel.Title
	}

	// The error belongs to the request that produced it, so it is dropped from
	// the URLs this page's forms post back to rather than being carried on.
	here := dropParams(r.URL.RequestURI(), "error")

	a.render(w, "index.html", pageData{
		Title:      title,
		User:       user,
		Items:      items,
		Panel:      panel,
		Tags:       tags,
		Lists:      lists,
		Counts:     counts,
		Filter:     f,
		Error:      q.Get("error"),
		Return:     here,
		ListReturn: dropParams(here, "panel"),
	})
}

// panelItem resolves the ?panel= id. The listed rows already carry the item and
// its tags, so it only falls back to the store when the panel item is filtered
// out of the current view. An unknown or stale id just means no panel.
func (a *app) panelItem(userID int64, raw string, items []Item) (*Item, error) {
	if raw == "" {
		return nil, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, nil
	}
	for i := range items {
		if items[i].ID == id {
			return &items[i], nil
		}
	}
	item, err := a.store.Get(userID, id)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// handleCreate saves a new item from the add form.
func (a *app) handleCreate(w http.ResponseWriter, r *http.Request) {
	// No ParseForm here: it only reads urlencoded bodies, and calling it leaves
	// r.Form non-nil, which stops FormValue from parsing a multipart one.
	back := safeReturn(r.FormValue("return"))

	item, err := a.itemFromForm(r, Item{})
	if err != nil {
		http.Redirect(w, r, withError(back, err.Error()), http.StatusSeeOther)
		return
	}

	id, err := a.store.Create(currentUser(r).ID, item)
	if err != nil {
		a.serverError(w, err)
		return
	}
	// Open the new item's panel when there is something to write about it.
	if item.Notes != "" {
		back = panelURL(back, id)
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// handleShow keeps /items/{id} working as a permalink: it opens the list with
// that item's panel in front of it.
func (a *app) handleShow(w http.ResponseWriter, r *http.Request) {
	id, ok := a.itemID(w, r)
	if !ok {
		return
	}
	if err := a.store.Exists(currentUser(r).ID, id); err != nil {
		a.mutationError(w, r, err)
		return
	}
	http.Redirect(w, r, panelURL("/", id), http.StatusSeeOther)
}

// handleUpdate saves edits to an item.
func (a *app) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := a.itemID(w, r)
	if !ok {
		return
	}
	back := safeReturn(r.FormValue("return"))

	item, err := a.itemFromForm(r, Item{ID: id})
	if err != nil {
		http.Redirect(w, r, withError(back, err.Error()), http.StatusSeeOther)
		return
	}

	switch err := a.store.Update(currentUser(r).ID, item); {
	case errors.Is(err, ErrNotFound):
		http.NotFound(w, r)
		return
	case err != nil:
		a.serverError(w, err)
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// itemFromForm reads and validates the shared item fields. A link is optional —
// plenty of books and films have none — but an item needs a title or a link to
// work out a title from.
func (a *app) itemFromForm(r *http.Request, into Item) (Item, error) {
	link := strings.TrimSpace(r.FormValue("url"))
	if link != "" {
		normalized, err := normalizeURL(link)
		if err != nil {
			return Item{}, err
		}
		link = normalized
	}

	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" && link != "" {
		title = fetchTitle(link)
	}
	if title == "" {
		return Item{}, errors.New("give it a title, or a link to take one from")
	}

	into.Kind = normalizeKind(r.FormValue("kind"))
	into.URL = link
	into.Title = title
	into.Creator = strings.TrimSpace(r.FormValue("creator"))
	into.Notes = strings.TrimSpace(r.FormValue("notes"))
	into.Status = normalizeStatus(r.FormValue("status"))
	into.Tags = parseTags(r.FormValue("tags"))
	return into, nil
}

// mutate runs a one-shot change to a single item and returns to the list the
// form came from. The routes below differ only in that change.
func (a *app) mutate(w http.ResponseWriter, r *http.Request, do func(userID, id int64) error) {
	id, ok := a.itemID(w, r)
	if !ok {
		return
	}
	if err := do(currentUser(r).ID, id); err != nil {
		a.mutationError(w, r, err)
		return
	}
	http.Redirect(w, r, safeReturn(r.FormValue("return")), http.StatusSeeOther)
}

// handleStatus moves an item along the queue.
func (a *app) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := normalizeStatus(r.FormValue("status"))
	a.mutate(w, r, func(userID, id int64) error { return a.store.SetStatus(userID, id, status) })
}

// handleFavorite flips the star on an item.
func (a *app) handleFavorite(w http.ResponseWriter, r *http.Request) {
	a.mutate(w, r, a.store.ToggleFavorite)
}

// handleDelete removes an item for good.
func (a *app) handleDelete(w http.ResponseWriter, r *http.Request) {
	a.mutate(w, r, a.store.Delete)
}

// itemID pulls the {id} path value, answering 404 when it is not a number.
func (a *app) itemID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return 0, false
	}
	return id, true
}

func (a *app) mutationError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	a.serverError(w, err)
}

func (a *app) render(w http.ResponseWriter, page string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.tmpl.ExecuteTemplate(w, page, data); err != nil {
		log.Printf("render %s: %v", page, err)
	}
}

func (a *app) serverError(w http.ResponseWriter, err error) {
	log.Printf("error: %v", err)
	http.Error(w, "Something went wrong.", http.StatusInternalServerError)
}

// normalizeURL validates a pasted link and adds a scheme when it is missing.
func normalizeURL(raw string) (string, error) {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", errors.New("that does not look like a link")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("only http and https links are supported")
	}
	return u.String(), nil
}

// safeReturn keeps redirects on this site.
func safeReturn(raw string) string {
	if strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		return raw
	}
	return "/"
}

// withError appends an error message to a local path.
func withError(path, msg string) string {
	return addParam(path, "error", msg)
}

// addParam sets a query parameter on a local path, replacing any existing one.
func addParam(path, key, value string) string {
	u, err := url.Parse(path)
	if err != nil {
		return path
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return u.String()
}

// dropParams removes query parameters from a local path.
func dropParams(path string, keys ...string) string {
	u, err := url.Parse(path)
	if err != nil {
		return path
	}
	q := u.Query()
	for _, k := range keys {
		q.Del(k)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
