// Copyright (C) 2023-2026 Òscar Casajuana Alonso

package grabber

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

//go:generate python3 ../scripts/search-sites.py
//go:embed search-sites.tsv
var searchSites string

type SeriesMatch struct {
	Title     string `json:"title"`
	URL       string `json:"url"`
	Source    string `json:"source"`
	Language  string `json:"language"`
	Thumbnail string `json:"thumbnail,omitempty"`
}

type SearchResults struct {
	Matches  []SeriesMatch `json:"matches"`
	Warnings []string      `json:"warnings,omitempty"`
	NeedsKey bool          `json:"needsKey"`
}

func searchKeyPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "manga-downloader", "brave-search-key"), nil
}

func braveKey() (string, error) {
	if key := strings.TrimSpace(os.Getenv("BRAVE_SEARCH_API_KEY")); key != "" {
		return key, nil
	}
	path, err := searchKeyPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return strings.TrimSpace(string(data)), err
}

// SaveSearchKey replaces the user's private key file atomically. An empty key removes it.
func SaveSearchKey(key string) error {
	path, err := searchKeyPath()
	if err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		err = os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if strings.ContainsAny(key, "\r\n") {
		return errors.New("invalid API key")
	}
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".brave-key-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(key)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

var searchClient = &http.Client{
	Timeout: 15 * time.Second,
	// Keep the subscription header on the API origin.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

type searchBatch struct {
	lang  string
	hosts map[string]string
	rules string
}

func searchLanguageAllowed(hint, lang string) bool {
	languages := strings.Split(hint, ",")
	return hint == "mixed" || slices.Contains(languages, lang) || (lang == "pt-br" && slices.Contains(languages, "pt"))
}

// Inline Goggles restrict the index before ranking. Split long rules into
// separate requests so encoded GET URLs fit common 8 KiB request-line limits.
func searchBatches(lang string) []searchBatch {
	var batches []searchBatch
	b := searchBatch{lang: lang, hosts: map[string]string{}, rules: "$discard"}
	for line := range strings.Lines(searchSites) {
		host, hint, _ := strings.Cut(strings.TrimSpace(line), "\t")
		if host == "mangeek.app" || !searchLanguageAllowed(hint, lang) {
			continue
		}
		rule := "\n$boost,site=" + host
		if len(url.QueryEscape(b.rules+rule)) > 4800 {
			batches = append(batches, b)
			b = searchBatch{lang: lang, hosts: map[string]string{}, rules: "$discard"}
		}
		b.hosts[host] = hint
		b.rules += rule
	}
	if len(b.hosts) > 0 {
		batches = append(batches, b)
	}
	return batches
}

type braveHit struct {
	Title, URL, Language string
	Thumbnail            struct {
		Src  string
		Logo bool
	}
}

func safeSearchURL(raw string) *url.URL {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil
	}
	return u
}

func (b searchBatch) match(h braveHit) (SeriesMatch, bool) {
	u := safeSearchURL(h.URL)
	if u == nil || u.Port() != "" {
		return SeriesMatch{}, false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	hint, ok := b.hosts[host]
	if !ok {
		return SeriesMatch{}, false
	}
	lang := strings.ToLower(strings.ReplaceAll(h.Language, "_", "-"))
	switch {
	case lang == "" && hint == b.lang:
	case b.lang == "en" && (lang == "en" || strings.HasPrefix(lang, "en-")):
	case b.lang == "pt-br" && (lang == "pt-br" || (lang == "pt" && (hint == "pt-br" || strings.Contains(strings.ToLower(u.Path), "/pt-br/") || strings.HasSuffix(host, ".br")))):
	default:
		return SeriesMatch{}, false
	}
	title := sanitizeTitle(html.UnescapeString(h.Title))
	if title == "" {
		return SeriesMatch{}, false
	}
	u.Fragment = ""
	m := SeriesMatch{Title: title, URL: u.String(), Language: b.lang, Source: host}
	if thumb := safeSearchURL(h.Thumbnail.Src); thumb != nil && thumb.Scheme == "https" && !h.Thumbnail.Logo {
		m.Thumbnail = thumb.String()
	}
	return m, true
}

func (b searchBatch) search(ctx context.Context, client *http.Client, key, query string) ([]SeriesMatch, error) {
	country := "US"
	if b.lang == "pt-br" {
		country = "BR"
	}
	params := url.Values{
		"q": {query}, "goggles": {b.rules}, "search_lang": {b.lang}, "country": {country},
		"count": {"20"}, "result_filter": {"web"}, "text_decorations": {"false"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.search.brave.com/res/v1/web/search?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Subscription-Token", key)
	req.Header.Set("Accept", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Brave Search is unreachable: %w", errors.Unwrap(err))
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Brave Search returned HTTP %d (%s)", res.StatusCode, http.StatusText(res.StatusCode))
	}
	var data struct{ Web struct{ Results []braveHit } }
	if err = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&data); err != nil {
		return nil, fmt.Errorf("invalid Brave Search response: %w", err)
	}
	var matches []SeriesMatch
	for _, h := range data.Web.Results {
		if m, ok := b.match(h); ok {
			matches = append(matches, m)
		}
	}
	return matches, nil
}

// SearchSeries searches both languages, retaining successful providers when
// another fails. Without a subscription, the native ManGeek catalogue works.
func SearchSeries(query string) (SearchResults, error) {
	out := SearchResults{Matches: []SeriesMatch{}}
	query = strings.TrimSpace(query)
	if query == "" {
		return out, errors.New("enter a manga title")
	}
	if len([]rune(query)) > 600 || len(strings.Fields(query)) > 75 {
		return out, errors.New("use a title of at most 600 characters and 75 words")
	}
	key, keyErr := braveKey()
	native, err := SearchManGeek(query)
	if err != nil {
		out.Warnings = append(out.Warnings, err.Error())
	} else {
		out.Matches = append(out.Matches, native...)
	}
	if keyErr != nil {
		out.Warnings = append(out.Warnings, keyErr.Error())
	}
	if key == "" {
		out.NeedsKey = true
		if err != nil {
			return out, err
		}
		return out, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	seen := map[string]bool{}
	// Alternate languages so both are represented early in the result list.
	en, pt := searchBatches("en"), searchBatches("pt-br")
	var batches []searchBatch
	for i := 0; i < max(len(en), len(pt)); i++ {
		if i < len(en) {
			batches = append(batches, en[i])
		}
		if i < len(pt) {
			batches = append(batches, pt[i])
		}
	}
	for i, b := range batches {
		if i > 0 {
			time.Sleep(time.Second)
		}
		matches, e := b.search(ctx, searchClient, key, query)
		if e != nil {
			out.Warnings = append(out.Warnings, e.Error())
			if i == 0 && err != nil {
				return out, errors.New(strings.Join(out.Warnings, "; "))
			}
			break
		}
		for _, m := range matches {
			if !seen[m.URL] {
				seen[m.URL] = true
				out.Matches = append(out.Matches, m)
			}
		}
	}
	return out, nil
}
