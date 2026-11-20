// Copyright (C) 2023-2026 Òscar Casajuana Alonso

package grabber

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	projecthttp "github.com/elboletaire/manga-downloader/http"
)

type searchRoundTrip func(*http.Request) (*http.Response, error)

func (f searchRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestTavilySearchBatch(t *testing.T) {
	b := searchBatch{lang: "pt-br", hosts: map[string]string{"test.com.br": "pt-br", "multi.example": "mixed", "portuguese.example": "pt"}}
	client := http.Client{Transport: searchRoundTrip(func(req *http.Request) (*http.Response, error) {
		var payload struct {
			Query, Country, Language string
			Domains                  []string `json:"include_domains"`
			Depth                    string   `json:"search_depth"`
			Max                      int      `json:"max_results"`
			Filter                   bool     `json:"filter_by_language"`
			Images                   bool     `json:"include_images"`
			Descriptions             bool     `json:"include_image_descriptions"`
		}
		if req.Method != "POST" || req.URL.String() != "https://api.tavily.com/search" || req.Header.Get("Authorization") != "Bearer test-only" || req.Header.Get("Content-Type") != "application/json" {
			t.Fatal("invalid Tavily request")
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(payload.Domains, []string{"multi.example", "portuguese.example", "test.com.br"}) || payload.Query != "A & B" || payload.Country != "brazil" || payload.Language != "pt" || payload.Depth != "basic" || payload.Max != 20 || !payload.Filter || !payload.Images || !payload.Descriptions {
			t.Fatalf("unexpected payload: %+v", payload)
		}
		body := `{"images":[{"url":"https://img.example/unrelated"}],"results":[
   {"title":"A &amp; B","url":"https://www.test.com.br/serie/ab#top","images":[{"url":"javascript:bad"},{"url":"https://img.example/c.webp","description":"Cover"}]},
   {"title":"wrong region","url":"https://test.com.br/pt-pt/other"},
   {"title":"European edition","url":"https://test.com.br/title?lang=pt_PT"},
   {"title":"Brazilian edition","url":"https://multi.example/edition?locale=pt_BR"},
   {"title":"regional edition","url":"https://multi.example/pt-br/entry"},
   {"title":"unknown region","url":"https://portuguese.example/title"},
   {"title":"off site","url":"https://test.com.br.attacker.example/ch"},
   {"title":"off protocol","url":"file:///test.com.br"},
   {"title":"no cover","url":"https://test.com.br/test","favicon":"https://img.example/logo"}
  ]}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
	found, err := b.search(context.Background(), &client, "test-only", "A & B")
	if err != nil || len(found) != 4 {
		t.Fatalf("results=%+v, err=%v", found, err)
	}
	if found[0] != (SeriesMatch{Title: "A & B", URL: "https://www.test.com.br/serie/ab", Source: "test.com.br", Language: "pt-br", Thumbnail: "https://img.example/c.webp"}) || found[1].Source != "multi.example" || found[1].Thumbnail != "" || found[2].Thumbnail != "" || found[3].Thumbnail != "" {
		t.Fatalf("unexpected hits: %+v", found)
	}
}

func TestSearchSiteCoverage(t *testing.T) {
	langs := map[string]map[string]bool{}
	for _, lang := range []string{"en", "pt-br"} {
		langs[lang] = map[string]bool{}
		for _, b := range searchBatches(lang) {
			if len(b.hosts) == 0 || len(b.hosts) > 300 {
				t.Fatal("invalid Tavily domain batch")
			}
			for host := range b.hosts {
				if langs[lang][host] {
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
	t.Setenv("TAVILY_API_KEY", "test-only")
	old := searchClient
	t.Cleanup(func() { searchClient = old })
	requests := 0
	searchClient = &http.Client{Transport: searchRoundTrip(func(req *http.Request) (*http.Response, error) {
		requests++
		if requests == 2 {
			return &http.Response{StatusCode: http.StatusTooManyRequests, Body: http.NoBody}, nil
		}
		var payload struct {
			Domains  []string `json:"include_domains"`
			Language string
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil || len(payload.Domains) == 0 || payload.Language != "en" {
			t.Fatal("invalid English search")
		}
		hit := fmt.Sprintf(`{"title":"Sol","url":"https://%s/series/sol"}`, payload.Domains[0])
		body := `{"results":[` + hit + "," + hit + `]}`
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

func TestTavilyKeyStorage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("AppData", dir)
	t.Setenv("TAVILY_API_KEY", "")
	path, err := searchKeyPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRAVE_SEARCH_API_KEY", "old-provider")
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "brave-search-key"), []byte("old-provider"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := tavilyKey(); got != "" || err != nil {
		t.Fatal("read old provider credentials")
	}
	if err := SaveSearchKey(" test-only "); err != nil {
		t.Fatal(err)
	}
	if got, err := tavilyKey(); got != "test-only" || err != nil {
		t.Fatal("key was not saved")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("unexpected permissions")
	}
	t.Setenv("TAVILY_API_KEY", "env-key")
	if got, _ := tavilyKey(); got != "env-key" {
		t.Fatal("environment precedence")
	}
	if err := SaveSearchKey(""); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("key file still exists")
	}
}
