// Copyright (C) 2023-2026 Òscar Casajuana Alonso

package grabber

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"maps"
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
	return filepath.Join(dir, "manga-downloader", "tavily-search-key"), nil
}

func tavilyKey() (string, error) {
	if key := strings.TrimSpace(os.Getenv("TAVILY_API_KEY")); key != "" {
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
	f, err := os.CreateTemp(dir, ".tavily-key-*")
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
	// Keep the bearer token on the API origin.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

type searchBatch struct {
	lang  string
	hosts map[string]string
}

func searchLanguageAllowed(hint, lang string) bool {
	languages := strings.Split(hint, ",")
	return hint == "mixed" || slices.Contains(languages, lang) || (lang == "pt-br" && slices.Contains(languages, "pt"))
}

// Tavily accepts up to 300 include_domains in one search.
const tavilyDomainLimit = 300

func searchBatches(lang string) []searchBatch {
	var batches []searchBatch
	b := searchBatch{lang: lang, hosts: map[string]string{}}
	for line := range strings.Lines(searchSites) {
		host, hint, _ := strings.Cut(strings.TrimSpace(line), "\t")
		if host == "mangeek.app" || !searchLanguageAllowed(hint, lang) {
			continue
		}
		if len(b.hosts) == tavilyDomainLimit {
			batches = append(batches, b)
			b = searchBatch{lang: lang, hosts: map[string]string{}}
		}
		b.hosts[host] = hint
	}
	if len(b.hosts) > 0 {
		batches = append(batches, b)
	}
	return batches
}

type tavilyHit struct {
	Title, URL string
	Images     []struct{ URL string }
}

func safeSearchURL(raw string) *url.URL {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil
	}
	return u
}

func (b searchBatch) match(h tavilyHit) (SeriesMatch, bool) {
	u := safeSearchURL(h.URL)
	if u == nil || u.Port() != "" {
		return SeriesMatch{}, false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	hint, ok := b.hosts[host]
	if !ok {
		return SeriesMatch{}, false
	}
	// Tavily strictly filters Portuguese but does not distinguish its regions.
	if b.lang == "pt-br" {
		locales := strings.Split(strings.ToLower(strings.ReplaceAll(u.Path, "_", "-")), "/")
		params := u.Query()
		for _, key := range []string{"lang", "language", "locale"} {
			locales = append(locales, strings.ToLower(strings.ReplaceAll(params.Get(key), "_", "-")))
		}
		if slices.Contains(locales, "pt-pt") || (hint != "pt-br" && !strings.HasSuffix(host, ".br") && !slices.Contains(locales, "pt-br")) {
			return SeriesMatch{}, false
		}
	}
	title := sanitizeTitle(html.UnescapeString(h.Title))
	if title == "" {
		return SeriesMatch{}, false
	}
	u.Fragment = ""
	m := SeriesMatch{Title: title, URL: u.String(), Language: b.lang, Source: host}
	for _, image := range h.Images {
		if thumb := safeSearchURL(image.URL); thumb != nil && thumb.Scheme == "https" {
			m.Thumbnail = thumb.String()
			break
		}
	}
	return m, true
}

func (b searchBatch) search(ctx context.Context, client *http.Client, key, query string) ([]SeriesMatch, error) {
	payload := map[string]any{
		"query":                      query,
		"include_domains":            slices.Sorted(maps.Keys(b.hosts)),
		"max_results":                20,
		"search_depth":               "basic",
		"language":                   b.lang,
		"filter_by_language":         true,
		"include_images":             true,
		"include_image_descriptions": true,
	}
	if b.lang == "pt-br" {
		payload["language"] = "pt"
		payload["country"] = "brazil"
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.tavily.com/search", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Tavily is unreachable: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		switch res.StatusCode {
		case 401:
			return nil, errors.New("Tavily rejected the API key (HTTP 401)")
		case 432, 433:
			return nil, fmt.Errorf("Tavily search credit limit reached (HTTP %d)", res.StatusCode)
		}
		return nil, fmt.Errorf("Tavily returned HTTP %d", res.StatusCode)
	}
	var data struct{ Results []tavilyHit }
	if err = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&data); err != nil {
		return nil, fmt.Errorf("invalid Tavily response: %w", err)
	}
	var matches []SeriesMatch
	for _, h := range data.Results {
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
	key, keyErr := tavilyKey()
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
