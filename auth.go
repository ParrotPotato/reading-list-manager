package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

// sessionCookie is the cookie the browser carries a session token in.
const sessionCookie = "catalogue_session"

// ctxUser is the key the signed-in account is stashed under for handlers.
type ctxKey struct{}

var ctxUser ctxKey

// currentUser returns the account this request is acting as. Handlers only run
// behind requireLogin, so this is always present.
func currentUser(r *http.Request) User {
	user, _ := r.Context().Value(ctxUser).(User)
	return user
}

// requireLogin lets a request through only when it carries a live session,
// and hands the account down to the handler.
func (a *app) requireLogin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			a.askToLogIn(w, r)
			return
		}
		user, ok := a.store.SessionUser(cookie.Value)
		if !ok {
			// A stale or forged token: clear it so the browser stops sending it.
			http.SetCookie(w, a.sessionCookie(r, "", time.Unix(0, 0)))
			a.askToLogIn(w, r)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxUser, user)))
	}
}

// askToLogIn sends an unauthenticated request to the login page, remembering
// where it was headed so the login can return it there.
func (a *app) askToLogIn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		// A POST from an expired session would lose its body on redirect, so
		// say so plainly rather than silently dropping the change.
		http.Error(w, "Your session has expired. Reload the page and sign in again.",
			http.StatusUnauthorized)
		return
	}
	to := "/login"
	if next := r.URL.RequestURI(); next != "/" {
		to = addParam(to, "next", next)
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// handleLoginForm shows the sign-in page.
func (a *app) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	// Already signed in: nothing to do here.
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if _, ok := a.store.SessionUser(cookie.Value); ok {
			http.Redirect(w, r, safeReturn(r.URL.Query().Get("next")), http.StatusSeeOther)
			return
		}
	}
	a.renderLogin(w, r, "")
}

// handleLogin checks the credentials and starts a session.
func (a *app) handleLogin(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	next := safeReturn(r.FormValue("next"))

	user, err := a.store.Authenticate(email, r.FormValue("password"))
	if errors.Is(err, ErrBadLogin) {
		log.Printf("failed login for %q from %s", email, r.RemoteAddr)
		a.renderLogin(w, r, ErrBadLogin.Error())
		return
	}
	if err != nil {
		a.serverError(w, err)
		return
	}

	token, expires, err := a.store.StartSession(user.ID)
	if err != nil {
		a.serverError(w, err)
		return
	}
	http.SetCookie(w, a.sessionCookie(r, token, expires))
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// handleLogout ends the session and returns to the login page.
func (a *app) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if err := a.store.EndSession(cookie.Value); err != nil {
			log.Printf("end session: %v", err)
		}
	}
	http.SetCookie(w, a.sessionCookie(r, "", time.Unix(0, 0)))
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// sessionCookie builds the session cookie, or its deletion when value is empty.
func (a *app) sessionCookie(r *http.Request, value string, expires time.Time) *http.Cookie {
	cookie := &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		// Lax still arrives on ordinary top-level navigation, but is not sent
		// on cross-site form posts, which is what guards the mutating routes.
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
	}
	if value == "" {
		cookie.MaxAge = -1
	}
	return cookie
}

// isHTTPS reports whether the browser reached us over TLS, including when a
// reverse proxy such as Caddy terminated it and said so.
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// loginData is the dot for the login template.
type loginData struct {
	Title string
	Next  string
	Email string
	Error string
}

func (a *app) renderLogin(w http.ResponseWriter, r *http.Request, message string) {
	next := r.FormValue("next")
	if next == "" {
		next = r.URL.Query().Get("next")
	}
	data := loginData{
		Title: "Sign in",
		Next:  safeReturn(next),
		Email: strings.TrimSpace(r.FormValue("email")),
		Error: message,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if message != "" {
		w.WriteHeader(http.StatusUnauthorized)
	}
	if err := a.tmpl.ExecuteTemplate(w, "login.html", data); err != nil {
		log.Printf("render login: %v", err)
	}
}

// accountData is the dot for the account page.
type accountData struct {
	Title   string
	User    User
	Error   string
	Changed bool
}

// handleAccount shows the account page, where a signed-in user can change
// their own password.
func (a *app) handleAccount(w http.ResponseWriter, r *http.Request) {
	a.renderAccount(w, r, accountData{Changed: r.URL.Query().Get("changed") == "1"})
}

// handleChangePassword changes the signed-in user's own password. It proves the
// current password first, so a borrowed session cannot lock the owner out.
func (a *app) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	current := r.FormValue("current")
	next := r.FormValue("new")

	if next != r.FormValue("repeat") {
		a.renderAccount(w, r, accountData{Error: "the two new passwords do not match"})
		return
	}

	switch err := a.store.ChangePassword(user.ID, current, next); {
	case errors.Is(err, ErrWrongPassword):
		log.Printf("failed password change for %q from %s", user.Email, r.RemoteAddr)
		a.renderAccount(w, r, accountData{Error: err.Error()})
		return
	case err != nil && !isServerError(err):
		a.renderAccount(w, r, accountData{Error: err.Error()})
		return
	case err != nil:
		a.serverError(w, err)
		return
	}

	// Keep this session, drop every other one: changing your password should
	// boot anyone who had it.
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if err := a.store.EndOtherSessions(user.ID, cookie.Value); err != nil {
			log.Printf("end other sessions: %v", err)
		}
	}
	http.Redirect(w, r, "/account?changed=1", http.StatusSeeOther)
}

// isServerError separates "something broke" from "you typed it wrong", so only
// the former becomes a 500 and the latter is shown on the form.
func isServerError(err error) bool {
	var invalid invalidInput
	return !errors.As(err, &invalid)
}

func (a *app) renderAccount(w http.ResponseWriter, r *http.Request, data accountData) {
	data.Title = "Account"
	data.User = currentUser(r)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if data.Error != "" {
		w.WriteHeader(http.StatusBadRequest)
	}
	if err := a.tmpl.ExecuteTemplate(w, "account.html", data); err != nil {
		log.Printf("render account: %v", err)
	}
}
