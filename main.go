package main

import (
	"context"
	"embed"
	"flag"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// app holds everything the handlers need.
type app struct {
	store *Store
	tmpl  *template.Template
}

func main() {
	addr := flag.String("addr", "localhost:8080", "address to listen on")
	dbPath := flag.String("db", "reading-list.db", "path to the SQLite database file")
	addUser := flag.String("add-user", "", "create an account with this email, then exit")
	passwd := flag.String("passwd", "", "reset this email's password, then exit")
	listUsers := flag.Bool("list-users", false, "list the accounts, then exit")
	flag.Parse()

	store, err := Open(*dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer store.Close()

	// Account management runs as a one-shot command against the same database,
	// which is why there is no sign-up page anywhere in the app.
	switch {
	case *addUser != "":
		if err := addUserCommand(store, *addUser); err != nil {
			log.Fatalf("add user: %v", err)
		}
		return
	case *passwd != "":
		if err := passwdCommand(store, *passwd); err != nil {
			log.Fatalf("change password: %v", err)
		}
		return
	case *listUsers:
		if err := listUsersCommand(store); err != nil {
			log.Fatalf("list users: %v", err)
		}
		return
	}

	tmpl, err := parseTemplates()
	if err != nil {
		log.Fatalf("parse templates: %v", err)
	}

	users, err := store.Users()
	if err != nil {
		log.Fatalf("read users: %v", err)
	}
	if len(users) == 0 {
		// os.Args[0] under "go run" is a temp build path, which is no use to
		// anyone reading this; the base name is what they actually typed.
		log.Printf("no accounts yet — create one with: %s -db %s -add-user <email>",
			filepath.Base(os.Args[0]), *dbPath)
	}

	a := &app{store: store, tmpl: tmpl}

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatalf("static files: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))

	// The only routes reachable without a session.
	mux.HandleFunc("GET /login", a.handleLoginForm)
	mux.HandleFunc("POST /login", a.handleLogin)
	mux.HandleFunc("POST /logout", a.handleLogout)
	// A shared list is public by design; its random token is the only key.
	mux.HandleFunc("GET /s/{token}", a.handlePublicList)

	// Everything else is somebody's catalogue, so it needs one.
	in := a.requireLogin
	mux.HandleFunc("GET /{$}", in(a.handleIndex))
	mux.HandleFunc("GET /account", in(a.handleAccount))
	mux.HandleFunc("POST /account/password", in(a.handleChangePassword))
	mux.HandleFunc("GET /lists", in(a.handleLists))
	mux.HandleFunc("POST /lists", in(a.handleCreateList))
	mux.HandleFunc("POST /lists/{id}", in(a.handleUpdateList))
	mux.HandleFunc("POST /lists/{id}/link", in(a.handleNewListLink))
	mux.HandleFunc("POST /lists/{id}/link/delete", in(a.handleClearListLink))
	mux.HandleFunc("POST /lists/{id}/delete", in(a.handleDeleteList))
	mux.HandleFunc("POST /items", in(a.handleCreate))
	mux.HandleFunc("GET /items/{id}", in(a.handleShow))
	mux.HandleFunc("POST /items/{id}", in(a.handleUpdate))
	mux.HandleFunc("POST /items/{id}/status", in(a.handleStatus))
	mux.HandleFunc("POST /items/{id}/favorite", in(a.handleFavorite))
	mux.HandleFunc("POST /items/{id}/lists/{list}", in(a.handleToggleListItem))
	mux.HandleFunc("POST /items/{id}/delete", in(a.handleDelete))
	// The catalogue used to be articles-only; old bookmarks still resolve.
	mux.HandleFunc("GET /articles/{id}", in(a.handleShow))

	srv := &http.Server{
		Addr:         *addr,
		Handler:      logRequests(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	// docker stop and systemctl stop both send SIGTERM. Left unhandled it ends the
	// process on the spot, the deferred store.Close never runs, and the data stays
	// in the -wal file rather than being checkpointed into the .db.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("catalogue at http://%s (database: %s)", *addr, *dbPath)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()
	log.Printf("shutting down")

	// Shorter than the ten seconds docker stop allows before it sends SIGKILL.
	shutdown, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

// parseTemplates parses every template into one shared set.
func parseTemplates() (*template.Template, error) {
	return template.New("").Funcs(templateFuncs()).ParseFS(templateFS, "templates/*.html")
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"since":       humanSince,
		"date":        func(t time.Time) string { return t.Format("2 Jan 2006") },
		"statusLabel": statusLabel,
		"lower":       strings.ToLower,
		"kindLabel":   func(slug string) string { return kindBySlug(slug).Label },
		"kinds":       func() []Kind { return Kinds },
		"statuses":    func() []Status { return Statuses },
		"sorts":       func() []Sort { return Sorts },
	}
}

// humanSince renders a timestamp as a short relative age.
func humanSince(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour")
	case d < 30*24*time.Hour:
		return plural(int(d.Hours()/24), "day")
	case d < 365*24*time.Hour:
		return plural(int(d.Hours()/(24*30)), "month")
	default:
		return plural(int(d.Hours()/(24*365)), "year")
	}
}

func plural(n int, unit string) string {
	if n != 1 {
		unit += "s"
	}
	return strconv.Itoa(n) + " " + unit + " ago"
}

// logRequests prints one line per request.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}
