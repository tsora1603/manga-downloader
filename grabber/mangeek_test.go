// Copyright (C) 2023-2026 Òscar Casajuana Alonso

package grabber

import (
	"io"
	nethttp "net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/elboletaire/manga-downloader/http"
)

type manGeekTransport struct {
	t       *testing.T
	routes  []string
	primary string
	mirror  string
}

func (rt *manGeekTransport) RoundTrip(req *nethttp.Request) (*nethttp.Response, error) {
	rt.t.Helper()
	path := strings.Split(strings.TrimPrefix(req.URL.Path, "/api/v2/pt/"), "/")
	if req.URL.Host != "geekstations.com.br" || len(path) != 4 || !regexp.MustCompile(`^[0-9A-F]+$`).MatchString(path[1]) {
		rt.t.Fatalf("unexpected API URL: %s", req.URL)
	}
	route := path[0]
	rt.routes = append(rt.routes, route)
	body := ""
	signature := "73/1a02912b21d8a0815bd5472589ae572c"
	switch route {
	case "manga":
		signature = "42/0576a00d2337f6109e6f4bdfdc0881f8"
		body = `{"title":"Minha série","chapters":[{"id":73,"title":"Capítulo 7,5"},{"id":80,"title":"Extra"}]}`
	case "chapter":
		body = rt.primary
	case "mirror":
		body = rt.mirror
	default:
		rt.t.Fatalf("unexpected API route: %s", route)
	}
	if strings.Join(path[2:], "/") != signature {
		rt.t.Fatalf("wrong ID or signature: %v", path)
	}
	return &nethttp.Response{StatusCode: 200, Header: nethttp.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

func TestManGeekSeries(t *testing.T) {
	const pages = `{"pages":["http://51.79.78.152/02.jpg?k=1","https://cdn.example/01.jpg"]}`
	for _, tc := range []struct {
		name, primary, mirror, routes string
		wantErr                       bool
	}{
		{"primary", pages, "", "manga chapter", false},
		{"empty primary", `{"pages":[]}`, pages, "manga chapter mirror", false},
		{"invalid primary", "error", pages, "manga chapter mirror", false},
		{"both invalid", `{"pages":["bad"]}`, `{"pages":[]}`, "manga chapter mirror", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := &manGeekTransport{t: t, primary: tc.primary, mirror: tc.mirror}
			http.SetRoundTripper(rt)
			t.Cleanup(func() { http.SetRoundTripper(nil) })
			site, errs := NewSite("https://mangeek.app/manga/42/?lang=pt", &Settings{})
			if len(errs) > 0 || site == nil {
				t.Fatalf("identify: %v", errs)
			}
			title, err := site.FetchTitle()
			if err != nil || title != "Minha série" {
				t.Fatalf("title: %q %v", title, err)
			}
			list, errs := site.FetchChapters()
			if len(errs) > 0 || len(list) != 2 || list[0].GetNumber() != 7.5 || !Unnumbered(list[1]) {
				t.Fatalf("listing: %v, %v", list, errs)
			}
			c, err := site.FetchChapter(list[0])
			if (err != nil) != tc.wantErr || strings.Join(rt.routes, " ") != tc.routes {
				t.Fatalf("routes %v, error %v", rt.routes, err)
			}
			if !tc.wantErr && (c.ID != "73" || c.Language != "pt-BR" || c.PagesCount != 2 || c.Pages[0].URL != "https://static.geekstations.com.br/02.jpg?k=1" || c.Pages[1].URL != "https://cdn.example/01.jpg" || c.Pages[1].Number != 2) {
				t.Fatalf("chapter: %+v", c)
			}
		})
	}
}

func TestManGeekReaderLink(t *testing.T) {
	site, _ := NewSite("https://mangeek.app/chapter/73", &Settings{})
	if _, ok := site.(*ManGeek); !ok {
		t.Fatalf("identified %T", site)
	}
	if _, err := site.FetchTitle(); err == nil || !strings.Contains(err.Error(), "try the series link") {
		t.Fatalf("unexpected error %v", err)
	}
	for _, raw := range []string{"https://mangeek.app.example/manga/42", "https://mangeek.app/manga/abc"} {
		m := &ManGeek{Grabber: &Grabber{URL: raw}}
		if ok, _ := m.Test(); ok {
			t.Errorf("claimed %s", raw)
		}
	}
}

type manGeekSearchTransport struct{ t *testing.T }

func (rt manGeekSearchTransport) RoundTrip(req *nethttp.Request) (*nethttp.Response, error) {
	rt.t.Helper()
	parts := strings.Split(req.URL.EscapedPath(), "/")
	if req.Method != "POST" || len(parts) != 8 || parts[4] != "search" ||
		parts[6] != "caf%C3%A9%20%2F%20sol" || parts[7] != "ed2d5199693c50afde0178a1b20f6cf5" {
		rt.t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil || string(body) != `{"tags":[]}` || req.Header.Get("Content-Type") != "application/json" {
		rt.t.Fatalf("search payload: %q, %v", body, err)
	}
	const result = `[{"id":7,"title":"Café"},{"id":7,"title":"Duplicate"},{"id":8,"title":"Sol"},{"title":"No ID"}]`
	return &nethttp.Response{StatusCode: 200, Header: nethttp.Header{}, Body: io.NopCloser(strings.NewReader(result)), Request: req}, nil
}

func TestSearchManGeek(t *testing.T) {
	http.SetRoundTripper(manGeekSearchTransport{t})
	t.Cleanup(func() { http.SetRoundTripper(nil) })
	if _, err := SearchManGeek("  "); err == nil {
		t.Fatal("empty query accepted")
	}
	matches, err := SearchManGeek("  CAFÉ / Sol  ")
	if err != nil || len(matches) != 2 || matches[0] != (ManGeekResult{"Café", "https://mangeek.app/manga/7"}) || matches[1].Title != "Sol" {
		t.Fatalf("search results: %+v, %v", matches, err)
	}
}
