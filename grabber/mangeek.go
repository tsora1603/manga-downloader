// Copyright (C) 2023-2026 Òscar Casajuana Alonso

package grabber

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/elboletaire/manga-downloader/http"
)

// ManGeek uses the GeekStations Portuguese catalog. Series links contain a
// numeric ID, independent of the IDs used by the chapter and mirror routes.
type ManGeek struct {
	*Grabber
	series *manGeekSeries
}

type manGeekSeries struct {
	Title    string
	Chapters []struct {
		ID    int64
		Title string
	}
}

var manGeekPath = regexp.MustCompile(`^/(manga|chapter)/([0-9]+)/?$`)
var manGeekChapterNumber = regexp.MustCompile(`[0-9]+(?:[.,][0-9]+)?`)
var manGeekPacer = pacer{interval: time.Second / 3}

func (m *ManGeek) Test() (bool, error) {
	host, path, ok := chapterPath(m.URL)
	return ok && host == "mangeek.app" && manGeekPath.MatchString(path), nil
}

func (m *ManGeek) WaitPage() { manGeekPacer.wait() }

func manGeekRequest(route, id string, target any) error {
	manGeekPacer.wait()
	nonce := strings.ToUpper(strconv.FormatInt(time.Now().UnixMilli(), 16))
	key := md5.Sum([]byte("M<" + id + "#MANG33K>D"))
	endpoint := fmt.Sprintf("http://geekstations.com.br/api/v2/pt/%s/%s/%s/%x", route, nonce, id, key)
	body, err := http.Get(http.RequestParams{URL: endpoint, Accept: "application/json", Headers: map[string]string{"X-Requested-With": "XMLHttpRequest"}})
	if err != nil {
		return err
	}
	defer body.Close()
	return json.NewDecoder(body).Decode(target)
}

func (m *ManGeek) fetchSeries() (*manGeekSeries, error) {
	if m.series != nil {
		return m.series, nil
	}
	_, path, _ := chapterPath(m.URL)
	match := manGeekPath.FindStringSubmatch(path)
	// The public chapter response only lists images, without a parent series.
	if match == nil || match[1] != "manga" {
		return nil, errSeriesNotFound(m.URL)
	}
	var series manGeekSeries
	if err := manGeekRequest("manga", match[2], &series); err != nil {
		return nil, err
	}
	series.Title = sanitizeTitle(series.Title)
	if series.Title == "" {
		return nil, fmt.Errorf("ManGeek returned no title for series %s", match[2])
	}
	m.series = &series
	return m.series, nil
}

func (m *ManGeek) FetchTitle() (string, error) {
	s, err := m.fetchSeries()
	if err != nil {
		return "", err
	}
	return s.Title, nil
}

func (m *ManGeek) FetchChapters() (Filterables, []error) {
	s, err := m.fetchSeries()
	if err != nil {
		return nil, []error{err}
	}
	var chapters Filterables
	var errs []error
	for _, row := range s.Chapters {
		if row.ID <= 0 {
			errs = append(errs, fmt.Errorf("missing ManGeek chapter ID for %q", row.Title))
			continue
		}
		c := &Chapter{ID: strconv.FormatInt(row.ID, 10), Title: sanitizeTitle(row.Title), Language: "pt-BR"}
		number := manGeekChapterNumber.FindString(c.Title)
		if number == "" {
			c.Label.Style = LabelUnnumbered
		} else {
			c.Number, err = strconv.ParseFloat(strings.ReplaceAll(number, ",", "."), 64)
			if err != nil {
				errs = append(errs, err)
				continue
			}
		}
		chapters = append(chapters, c)
	}
	return chapters, errs
}

func (m *ManGeek) FetchChapter(f Filterable) (*Chapter, error) {
	c := *f.(*Chapter)
	var lastErr error
	for _, route := range []string{"chapter", "mirror"} {
		pages, err := manGeekPages(route, c.ID)
		if err != nil {
			lastErr = err
			continue
		}
		c.Pages, c.PagesCount = pages, int64(len(pages))
		return &c, nil
	}
	return nil, fmt.Errorf("ManGeek chapter %s: %w", c.ID, lastErr)
}

func manGeekPages(route, id string) ([]Page, error) {
	var data struct{ Pages []string }
	if err := manGeekRequest(route, id, &data); err != nil {
		return nil, err
	}
	if len(data.Pages) == 0 {
		return nil, fmt.Errorf("no pages on %s", route)
	}
	pages := make([]Page, 0, len(data.Pages))
	for i, raw := range data.Pages {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") {
			return nil, fmt.Errorf("invalid image URL at page %d on %s", i+1, route)
		}
		if u.Host == "51.79.78.152" {
			u.Host, u.Scheme = "static.geekstations.com.br", "https"
		}
		pages = append(pages, Page{Number: int64(i + 1), URL: u.String()})
	}
	return pages, nil
}
