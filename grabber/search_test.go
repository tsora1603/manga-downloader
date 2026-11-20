// Copyright (C) 2023-2026 Òscar Casajuana Alonso

package grabber

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"testing"

	projecthttp "github.com/elboletaire/manga-downloader/http"
)

type searchRoundTrip func(*http.Request) (*http.Response, error)

func (f searchRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestBraveSearchBatch(t *testing.T) {
	b := searchBatch{lang: "pt-br", hosts: map[string]string{"test.com.br": "pt-br", "multi.example": "mixed", "portuguese.example": "pt"}, rules: "$discard\n$boost,site=test.com.br\n$boost,site=multi.example\n$boost,site=portuguese.example"}
	client := http.Client{Transport: searchRoundTrip(func(req *http.Request) (*http.Response, error) {
		q := req.URL.Query()
		if req.URL.Scheme != "https" || req.URL.Host != "api.search.brave.com" || q.Get("goggles") != b.rules || q.Get("q") != "A & B" || q.Get("country") != "BR" || q.Get("search_lang") != b.lang || req.Header.Get("X-Subscription-Token") != "test-only" || strings.Contains(req.URL.String(), "test-only") {
			t.Fatal("invalid search request")
		}
		body := `{"web":{"results":[
		{"title":"A &amp; B","url":"https://www.test.com.br/serie/ab#top","language":"pt","thumbnail":{"src":"https://img.example/c.webp"}},
		{"title":"wrong language","url":"https://test.com.br/serie/other","language":"pt-PT"},
		{"title":"regional edition","url":"https://multi.example/pt-br/entry","language":"pt"},
		{"title":"unknown language","url":"https://multi.example/other"},
		{"title":"unknown region","url":"https://portuguese.example/title","language":"pt"},
		{"title":"off site","url":"https://test.com.br.attacker.example/ch","language":"pt-br"},
		{"title":"off protocol","url":"file:///test.com.br"},
		{"title":"no cover","url":"https://test.com.br/test","thumbnail":{"src":"javascript:bad"}},
		{"title":"site logo","url":"https://test.com.br/logo","thumbnail":{"src":"https://img.example/logo","logo":true}}
		]}}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
	found, err := b.search(context.Background(), &client, "test-only", "A & B")
	if err != nil || len(found) != 4 {
		t.Fatalf("results=%+v, err=%v", found, err)
	}
	if found[0] != (SeriesMatch{Title: "A & B", URL: "https://www.test.com.br/serie/ab", Source: "test.com.br", Language: "pt-br", Thumbnail: "https://img.example/c.webp"}) || found[2].Thumbnail != "" || found[3].Thumbnail != "" {
		t.Fatalf("unexpected hits: %+v", found)
	}
}

func TestSearchSiteCoverage(t *testing.T) {
	langs := map[string]map[string]bool{}
	for _, lang := range []string{"en", "pt-br"} {
		langs[lang] = map[string]bool{}
		for _, b := range searchBatches(lang) {
			if !strings.HasPrefix(b.rules, "$discard\n") || len(url.QueryEscape(b.rules)) > 4800 {
				t.Fatal("invalid Goggle")
			}
			for host := range b.hosts {
				if langs[lang][host] || !strings.Contains(b.rules, "\n$boost,site="+host) {
					t.Fatal(host)
				}
				langs[lang][host] = true
			}
		}
	}
	// Catch stale registry entries whenever the source-of-truth table changes.
	data, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(data), "## Supported sites\n")
	if !ok {
		t.Fatal("missing site list")
	}
	section, _, _ = strings.Cut(section, "\n## ")
	declared := map[string]bool{}
	for line := range strings.Lines(section) {
		if !strings.HasPrefix(line, "| [") {
			continue
		}
		label, rest, _ := strings.Cut(line[3:], "](")
		target, _, _ := strings.Cut(rest, ")")
		u, err := url.Parse(target)
		if err != nil {
			t.Fatal(err)
		}
		for _, host := range []string{label, u.Hostname()} {
			declared[strings.TrimPrefix(strings.ToLower(host), "www.")] = true
		}
	}
	for line := range strings.Lines(searchSites) {
		host, hint, _ := strings.Cut(strings.TrimSpace(line), "\t")
		if !declared[host] {
			t.Errorf("unexpected registry host %s", host)
		}
		delete(declared, host)
		for lang, hosts := range langs {
			want := host != "mangeek.app" && searchLanguageAllowed(hint, lang)
			if hosts[host] != want {
				t.Errorf("coverage of %s for %s", host, lang)
			}
		}
	}
	if len(declared) > 0 {
		t.Fatalf("regenerate search-sites.tsv; missing %v", declared)
	}
}

func TestSearchSeriesKeepsPartialResults(t *testing.T) {
	projecthttp.SetRoundTripper(manGeekSearchTransport{t})
	t.Cleanup(func() { projecthttp.SetRoundTripper(nil) })
	t.Setenv("BRAVE_SEARCH_API_KEY", "test-only")
	old := searchClient
	t.Cleanup(func() { searchClient = old })
	requests := 0
	searchClient = &http.Client{Transport: searchRoundTrip(func(req *http.Request) (*http.Response, error) {
		requests++
		if requests == 2 {
			return &http.Response{StatusCode: http.StatusTooManyRequests, Body: http.NoBody}, nil
		}
		rules := strings.Split(req.URL.Query().Get("goggles"), "\n")
		host := strings.TrimPrefix(rules[1], "$boost,site=")
		hit := fmt.Sprintf(`{"title":"Sol","url":"https://%s/series/sol","language":"en"}`, host)
		body := `{"web":{"results":[` + hit + "," + hit + `]}}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	found, err := SearchSeries("Café / Sol")
	if err != nil || found.NeedsKey || requests != 2 || len(found.Matches) != 3 || len(found.Warnings) != 1 || !strings.Contains(found.Warnings[0], "429") {
		t.Fatalf("unexpected partial result: %+v, %v", found, err)
	}
	if found.Matches[0].Source != "mangeek.app" || found.Matches[2].Language != "en" {
		t.Fatalf("expected native and web results: %+v", found.Matches)
	}
}

func TestBraveKeyStorage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("AppData", dir)
	t.Setenv("BRAVE_SEARCH_API_KEY", "")
	if err := SaveSearchKey(" test-only "); err != nil {
		t.Fatal(err)
	}
	if got, err := braveKey(); got != "test-only" || err != nil {
		t.Fatal("key was not saved")
	}
	path, err := searchKeyPath()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("unexpected permissions")
	}
	t.Setenv("BRAVE_SEARCH_API_KEY", "env-key")
	if got, _ := braveKey(); got != "env-key" {
		t.Fatal("environment precedence")
	}
	if err := SaveSearchKey(""); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("key file still exists")
	}
}
