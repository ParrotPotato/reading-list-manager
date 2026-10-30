package main

import (
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// fetchTitle makes a best-effort attempt to read the <title> of a page so the
// reader can just paste a link. It falls back to the host name.
func fetchTitle(link string) string {
	fallback := link
	if host := hostOf(link); host != "" {
		fallback = host
	}

	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequest(http.MethodGet, link, nil)
	if err != nil {
		return fallback
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; reading-list/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return fallback
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fallback
	}

	// The <title> lives in the head, so a prefix of the body is plenty.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if err != nil {
		return fallback
	}
	m := titleRe.FindSubmatch(body)
	if m == nil {
		return fallback
	}
	title := strings.Join(strings.Fields(html.UnescapeString(string(m[1]))), " ")
	if title == "" {
		return fallback
	}
	return title
}
