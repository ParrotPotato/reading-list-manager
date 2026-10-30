package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// addUserCommand creates an account from the terminal. This is the only way an
// account comes into existence — the app has no sign-up page on purpose.
func addUserCommand(store *Store, email string) error {
	password, err := readNewPassword()
	if err != nil {
		return err
	}
	user, adopted, err := store.CreateUser(email, password)
	if err != nil {
		return err
	}
	fmt.Printf("Created account %q.\n", user.Email)
	if adopted {
		fmt.Println("Items saved before accounts existed are now owned by this account.")
	}
	return nil
}

// passwdCommand changes an existing account's password and signs it out
// everywhere, so a password change actually revokes access.
func passwdCommand(store *Store, email string) error {
	password, err := readNewPassword()
	if err != nil {
		return err
	}
	if err := store.SetPassword(email, password); err != nil {
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("no account for %q", email)
		}
		return err
	}
	if err := store.EndUserSessions(email); err != nil {
		return err
	}
	fmt.Printf("Password changed for %q. Existing sessions have been signed out.\n", email)
	return nil
}

// listUsersCommand prints the accounts that exist.
func listUsersCommand(store *Store) error {
	users, err := store.Users()
	if err != nil {
		return err
	}
	if len(users) == 0 {
		fmt.Println("No accounts yet.")
		return nil
	}
	for _, u := range users {
		fmt.Printf("%-32s created %s\n", u.Email, u.CreatedAt.Format("2 Jan 2006"))
	}
	return nil
}

// readNewPassword asks for a password twice, without echoing it.
func readNewPassword() (string, error) {
	first, err := readPassword("Password: ")
	if err != nil {
		return "", err
	}
	if err := checkPassword(first); err != nil {
		return "", err
	}
	second, err := readPassword("Repeat password: ")
	if err != nil {
		return "", err
	}
	if first != second {
		return "", errors.New("the two passwords do not match")
	}
	return first, nil
}

// stdin is shared across reads: a bufio.Reader buffers ahead, so a fresh one
// per call would swallow the lines the next call is waiting for.
var stdin = bufio.NewReader(os.Stdin)

// readPassword reads one line with echo off when stdin is a terminal, and
// plainly when it is not, so the command still works from a script.
func readPassword(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := stdin.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Print(prompt)
	raw, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
