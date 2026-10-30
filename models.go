package main

import (
	"net/url"
	"strings"
	"time"
)

// Kind is one of the fixed categories an item can belong to. The set is closed
// so that counts, grouping and sorting are reliable; Creator is what the form
// calls the person or outfit behind this sort of thing.
type Kind struct {
	Slug    string
	Label   string
	Creator string
}

// Kinds is the catalogue's vocabulary, in the order the UI shows it.
var Kinds = []Kind{
	{"article", "Article", "Author"},
	{"video", "Video", "Channel"},
	{"book", "Book", "Author"},
	{"film", "Film", "Director"},
	{"documentary", "Documentary", "Director"},
	{"podcast", "Podcast", "Show"},
	{"website", "Website", "Site"},
	{"other", "Other", "Creator"},
}

// DefaultKind is what an item becomes when nothing else is chosen.
const DefaultKind = "article"

func kindBySlug(slug string) Kind {
	for _, k := range Kinds {
		if k.Slug == slug {
			return k
		}
	}
	return Kind{Slug: slug, Label: slug, Creator: "Creator"}
}

// validKind reports whether s names a kind, returning its cleaned slug.
func validKind(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, k := range Kinds {
		if k.Slug == s {
			return s, true
		}
	}
	return "", false
}

// normalizeKind falls back to the default for anything unrecognized.
func normalizeKind(s string) string {
	if slug, ok := validKind(s); ok {
		return slug
	}
	return DefaultKind
}

// Status is where an item sits in the queue. The wording stays medium-neutral:
// a film is not "read".
type Status struct {
	Slug  string
	Label string
}

const (
	StatusQueued = "queued"
	StatusActive = "active"
	StatusDone   = "done"
)

// Statuses is the queue vocabulary, in progression order.
var Statuses = []Status{
	{StatusQueued, "Queued"},
	{StatusActive, "In progress"},
	{StatusDone, "Done"},
}

func statusLabel(slug string) string {
	for _, s := range Statuses {
		if s.Slug == slug {
			return s.Label
		}
	}
	return slug
}

// validStatus reports whether s names a status, returning its cleaned slug.
func validStatus(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, valid := range Statuses {
		if s == valid.Slug {
			return s, true
		}
	}
	return "", false
}

// normalizeStatus falls back to "queued" for anything unrecognized.
func normalizeStatus(s string) string {
	if slug, ok := validStatus(s); ok {
		return slug
	}
	return StatusQueued
}

// Sort is one of the orders the list can be shown in. The matching ORDER BY
// lives in the store, beside the query it is spliced into.
type Sort struct {
	Slug  string
	Label string
}

// DefaultSort is the order used when none is asked for.
const DefaultSort = "added"

// Sorts is the sort vocabulary, default first.
var Sorts = []Sort{
	{DefaultSort, "Recently added"},
	{"updated", "Recently updated"},
	{"title", "Title A–Z"},
	{"kind", "By category"},
}

func sortBySlug(slug string) Sort {
	for _, s := range Sorts {
		if s.Slug == slug {
			return s
		}
	}
	return sortBySlug(DefaultSort)
}

// Item is something saved to the catalogue: an article, a film, a book, a
// podcast — plus whatever notes were written about it.
type Item struct {
	ID        int64
	Kind      string
	URL       string
	Title     string
	Creator   string
	Notes     string
	Status    string
	Favorite  bool
	CreatedAt time.Time
	Tags      []string
	// Lists are the public lists this item is on, by title.
	Lists []ListRef
}

// InList reports whether the item is on the given list.
func (i Item) InList(id int64) bool {
	for _, l := range i.Lists {
		if l.ID == id {
			return true
		}
	}
	return false
}

// KindLabel is the display name of this item's category.
func (i Item) KindLabel() string { return kindBySlug(i.Kind).Label }

// CreatorLabel is what this kind of item calls its creator.
func (i Item) CreatorLabel() string { return kindBySlug(i.Kind).Creator }

// StatusLabel is the display name of this item's place in the queue.
func (i Item) StatusLabel() string { return statusLabel(i.Status) }

// Host is the domain the item lives on, for display in the list. Items with no
// link — most books and films — simply have none.
func (i Item) Host() string { return hostOf(i.URL) }

// hostOf is the bare domain of a link, without its leading "www.".
func hostOf(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Host, "www.")
}

// TagLine renders the tags the way the edit form wants them back.
func (i Item) TagLine() string { return strings.Join(i.Tags, ", ") }

// TagCount is a tag together with how many items use it.
type TagCount struct {
	Name  string
	Count int
}

// Filter narrows and orders the item list.
type Filter struct {
	Query    string
	Tag      string
	Kind     string
	Status   string
	Favorite bool
	List     int64
	Sort     string
}

// Active reports whether the filter narrows anything. Sort order is not a
// narrowing, so it does not count.
func (f Filter) Active() bool {
	return f.Query != "" || f.Tag != "" || f.Kind != "" || f.Status != "" || f.Favorite || f.List != 0
}

// SortIsDefault reports whether the order is the one a bare URL already gives,
// so forms can leave it out rather than spelling it into every link.
func (f Filter) SortIsDefault() bool { return f.Sort == DefaultSort }

// parseTags splits a comma separated tag line into clean, de-duplicated tags.
func parseTags(line string) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range strings.Split(line, ",") {
		t := strings.Join(strings.Fields(raw), " ")
		if t == "" {
			continue
		}
		key := strings.ToLower(t)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, t)
	}
	return out
}
