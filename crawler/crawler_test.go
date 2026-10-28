package crawler_test

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"code/crawler"
)

var update = flag.Bool("update", false, "перезаписать эталонный отчёт testdata/report.json")

const site = "http://site.test"

var startTime = time.Date(2024, 6, 1, 12, 34, 56, 0, time.UTC)

// fakeClock — время, которое двигается только ожиданием; паузы записываются.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func newClock() *fakeClock { return &fakeClock{now: startTime} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if d > 0 {
		c.now = c.now.Add(d)
		c.sleeps = append(c.sleeps, d)
	}

	return nil
}

// reply — ответ транспорта: код, тело, Content-Length (-1 — заголовка нет) или сетевая ошибка.
type reply struct {
	status int
	body   string
	length int64
	err    error
}

// transport отвечает по ключу «МЕТОД адрес», считает обращения и запоминает время каждого по часам.
type transport struct {
	mu      sync.Mutex
	replies map[string][]reply // последовательность ответов; последний повторяется
	calls   map[string]int
	times   []time.Time
	clock   *fakeClock
}

func newTransport(clock *fakeClock, replies map[string][]reply) *transport {
	return &transport{replies: replies, calls: map[string]int{}, clock: clock}
}

func (tr *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	key := req.Method + " " + req.URL.String()

	tr.mu.Lock()
	n := tr.calls[key]
	tr.calls[key]++
	tr.times = append(tr.times, tr.clock.Now())
	seq, ok := tr.replies[key]
	tr.mu.Unlock()

	if !ok {
		return nil, errors.New("no such host")
	}

	r := seq[min(n, len(seq)-1)]
	if r.err != nil {
		return nil, r.err
	}

	length := r.length
	if length == 0 && r.body != "" {
		length = int64(len(r.body))
	}

	return &http.Response{
		StatusCode:    r.status,
		Body:          io.NopCloser(strings.NewReader(r.body)),
		ContentLength: length,
		Header:        http.Header{},
		Request:       req,
	}, nil
}

func (tr *transport) count(key string) int {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	return tr.calls[key]
}

func fixture(t *testing.T, name string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)

	return string(data)
}

// siteReplies — сайт из testdata: три страницы, битая и внешняя ссылки, ассеты.
func siteReplies(t *testing.T) map[string][]reply {
	t.Helper()

	ok := []reply{{status: http.StatusOK}}

	return map[string][]reply{
		"GET " + site:                     {{status: http.StatusOK, body: fixture(t, "index.html")}},
		"GET " + site + "/about.html":     {{status: http.StatusOK, body: fixture(t, "about.html")}},
		"GET " + site + "/deep.html":      {{status: http.StatusOK, body: fixture(t, "deep.html")}},
		"HEAD " + site:                    ok,
		"HEAD " + site + "/about.html":    ok,
		"HEAD " + site + "/deep.html":     ok,
		"HEAD " + site + "/missing":       {{status: http.StatusNotFound}},
		"GET " + site + "/missing":        {{status: http.StatusNotFound}},
		"HEAD " + site + "/style.css":     {{status: http.StatusOK, length: 120}},
		"HEAD " + site + "/logo.png":      {{status: http.StatusOK, length: 2048}},
		"HEAD " + site + "/no-length.png": {{status: http.StatusOK, length: -1}},
		"GET " + site + "/no-length.png":  {{status: http.StatusOK, body: "0123456789", length: -1}},
		"HEAD " + site + "/app.js":        {{status: http.StatusInternalServerError}},
	}
}

func analyze(t *testing.T, opts crawler.Options) crawler.Report {
	t.Helper()

	data, err := crawler.Analyze(context.Background(), opts)
	require.NoError(t, err)

	var report crawler.Report
	require.NoError(t, json.Unmarshal(data, &report))

	return report
}

func options(clock *fakeClock, tr *transport, depth int) crawler.Options {
	return crawler.Options{
		URL:         site,
		Depth:       depth,
		Concurrency: 2,
		HTTPClient:  &http.Client{Transport: tr},
		Clock:       clock,
	}
}

func pageURLs(report crawler.Report) []string {
	urls := []string{}
	for _, page := range report.Pages {
		urls = append(urls, page.URL)
	}

	return urls
}

func TestAnalyzeMatchesGoldenReport(t *testing.T) {
	clock := newClock()
	opts := options(clock, newTransport(clock, siteReplies(t)), 3)
	opts.IndentJSON = true

	data, err := crawler.Analyze(context.Background(), opts)
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(string(data), "}\n"), "отчёт заканчивается переводом строки")

	golden := filepath.Join("testdata", "report.json")
	if *update {
		require.NoError(t, os.WriteFile(golden, data, 0o600))
	}

	require.JSONEq(t, fixture(t, "report.json"), string(data))
}

func TestAnalyzeIndentChangesOnlyFormatting(t *testing.T) {
	render := func(indent bool) string {
		clock := newClock()
		opts := options(clock, newTransport(clock, siteReplies(t)), 3)
		opts.IndentJSON = indent

		data, err := crawler.Analyze(context.Background(), opts)
		require.NoError(t, err)

		return string(data)
	}

	compact, indented := render(false), render(true)

	require.NotEqual(t, compact, indented)
	require.JSONEq(t, compact, indented)
	require.Equal(t, 1, strings.Count(compact, "\n"))
}

func TestAnalyzeDepth(t *testing.T) {
	tests := []struct {
		depth int
		want  []string
	}{
		{depth: 1, want: []string{site}},
		{depth: 2, want: []string{site, site + "/about.html", site + "/missing"}},
		{depth: 5, want: []string{site, site + "/about.html", site + "/missing", site + "/deep.html"}},
	}

	for _, tt := range tests {
		clock := newClock()
		tr := newTransport(clock, siteReplies(t))
		report := analyze(t, options(clock, tr, tt.depth))

		require.Equal(t, tt.depth, report.Depth)
		require.Equal(t, tt.want, pageURLs(report), "depth %d", tt.depth)

		wantDepth := map[string]int{site: 0, site + "/about.html": 1, site + "/missing": 1, site + "/deep.html": 2}
		for _, page := range report.Pages {
			require.Equal(t, wantDepth[page.URL], page.Depth, page.URL)
		}

		// Внешняя ссылка проверяется, но не обходится; дубликаты /about.html загружаются один раз.
		require.Zero(t, tr.count("GET https://external.test/page"))
		require.LessOrEqual(t, tr.count("GET "+site+"/about.html"), 1)
	}
}

func TestAnalyzeSEO(t *testing.T) {
	clock := newClock()
	report := analyze(t, options(clock, newTransport(clock, siteReplies(t)), 2))

	require.Equal(t, crawler.SEO{
		HasTitle:       true,
		Title:          "Home & Garden",
		HasDescription: true,
		Description:    `Tools "for" the garden`,
		HasH1:          true,
	}, report.Pages[0].SEO)
	require.Equal(t, crawler.SEO{}, report.Pages[1].SEO, "у страницы без тегов флаги false и пустые строки")
}

func TestAnalyzeBrokenLinks(t *testing.T) {
	clock := newClock()
	report := analyze(t, options(clock, newTransport(clock, siteReplies(t)), 1))

	require.Equal(t, []crawler.BrokenLink{
		{URL: site + "/missing", StatusCode: http.StatusNotFound, Error: "Not Found"},
		{URL: "https://external.test/page", Error: `Head "https://external.test/page": no such host`},
	}, report.Pages[0].BrokenLinks)
}

func TestAnalyzeAssets(t *testing.T) {
	clock := newClock()
	tr := newTransport(clock, siteReplies(t))
	report := analyze(t, options(clock, tr, 2))

	require.Equal(t, []crawler.Asset{
		{URL: site + "/logo.png", Type: "image", StatusCode: http.StatusOK, SizeBytes: 2048},
		{URL: site + "/no-length.png", Type: "image", StatusCode: http.StatusOK, SizeBytes: 10},
		{URL: site + "/app.js", Type: "script", StatusCode: http.StatusInternalServerError, Error: "Internal Server Error"},
		{URL: site + "/style.css", Type: "style", StatusCode: http.StatusOK, SizeBytes: 120},
	}, report.Pages[0].Assets)
	require.Equal(t, report.Pages[0].Assets[:1], report.Pages[1].Assets)

	// logo.png встречается на двух страницах и дважды на первой, но запрашивается один раз.
	require.Equal(t, 1, tr.count("HEAD "+site+"/logo.png"))
	require.Equal(t, 1, tr.count("GET "+site+"/no-length.png"))
}

func TestAnalyzeUnreachablePage(t *testing.T) {
	clock := newClock()
	tr := newTransport(clock, map[string][]reply{"GET " + site: {{status: http.StatusNotFound}}})
	report := analyze(t, options(clock, tr, 2))

	require.Equal(t, []crawler.Page{{
		URL: site, HTTPStatus: http.StatusNotFound, Status: "error", Error: "Not Found", DiscoveredAt: startTime,
	}}, report.Pages)
}

func TestAnalyzeRetries(t *testing.T) {
	failure := reply{err: errors.New("connection reset")}
	page := reply{status: http.StatusOK, body: fixture(t, "deep.html")}

	tests := []struct {
		name      string
		retries   int
		replies   []reply
		wantCalls int
		wantOK    bool
	}{
		{name: "success after one failure", retries: 1, replies: []reply{failure, page}, wantCalls: 2, wantOK: true},
		{name: "failures exhaust retries", retries: 1, replies: []reply{failure, failure, page}, wantCalls: 2},
		{name: "retry on 503", retries: 2, replies: []reply{{status: 503}, {status: 503}, page}, wantCalls: 3, wantOK: true},
		{name: "no retry on 404", retries: 3, replies: []reply{{status: 404}, page}, wantCalls: 1},
		{name: "no retries configured", retries: 0, replies: []reply{failure, page}, wantCalls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := newClock()
			tr := newTransport(clock, map[string][]reply{"GET " + site: tt.replies})
			opts := options(clock, tr, 1)
			opts.Retries = tt.retries

			report := analyze(t, opts)

			require.Equal(t, tt.wantCalls, tr.count("GET "+site))
			require.Equal(t, tt.wantOK, report.Pages[0].Status == "ok", report.Pages[0].Error)
			require.Len(t, clock.sleeps, tt.wantCalls-1, "перед каждым повтором — ненулевая пауза")
		})
	}
}

func TestAnalyzeRateLimit(t *testing.T) {
	tests := []struct {
		name     string
		delay    time.Duration
		rps      float64
		interval time.Duration
	}{
		{name: "delay", delay: 200 * time.Millisecond, interval: 200 * time.Millisecond},
		{name: "rps overrides delay", delay: time.Second, rps: 10, interval: 100 * time.Millisecond},
		{name: "no limit", interval: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := newClock()
			tr := newTransport(clock, siteReplies(t))
			opts := options(clock, tr, 3)
			// Один воркер: фейковые часы сдвигаются каждым ожиданием, и интервалы видны точно.
			// Общий для всех воркеров лимит проверяет тест лимитера в internal/fetch.
			opts.Delay, opts.RPS, opts.Concurrency = tt.delay, tt.rps, 1

			report := analyze(t, opts)
			require.Len(t, report.Pages, 4)

			// Интервал выдерживается между любыми запросами: страницами, ссылками и ассетами.
			require.Greater(t, len(tr.times), 10)
			for i := 1; i < len(tr.times); i++ {
				require.Equal(t, tt.interval, tr.times[i].Sub(tr.times[i-1]), "запрос %d", i)
			}

			if tt.interval == 0 {
				require.Empty(t, clock.sleeps)
			}
		})
	}
}

func TestAnalyzeCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	clock := newClock()
	data, err := crawler.Analyze(ctx, options(clock, newTransport(clock, siteReplies(t)), 3))

	require.ErrorIs(t, err, context.Canceled)

	var report crawler.Report
	require.NoError(t, json.Unmarshal(data, &report), "при отмене отчёт остаётся валидным JSON")
	require.Equal(t, site, report.RootURL)
	require.Empty(t, report.Pages)
}

func TestAnalyzeCancelStopsWaiting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	clock := newClock()
	opts := options(clock, newTransport(clock, siteReplies(t)), 3)
	opts.Clock = nil // настоящее время: ожидание прерывается отменой, а не истекает
	opts.Delay = time.Hour

	started := time.Now()
	data, err := crawler.Analyze(ctx, opts)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(started), 5*time.Second)
	require.True(t, json.Valid(data))
}

func TestAnalyzeHeadNotAllowedFallsBackToGet(t *testing.T) {
	clock := newClock()
	tr := newTransport(clock, map[string][]reply{
		"GET " + site:            {{status: http.StatusOK, body: `<a href="/page">x</a>`}},
		"HEAD " + site + "/page": {{status: http.StatusMethodNotAllowed}},
		"GET " + site + "/page":  {{status: http.StatusOK, body: "ok"}},
	})
	report := analyze(t, options(clock, tr, 1))

	require.Empty(t, report.Pages[0].BrokenLinks)
	require.Equal(t, 1, tr.count("GET "+site+"/page"))
}

func TestAnalyzeInvalidURL(t *testing.T) {
	for _, raw := range []string{"", "example.com", "ftp://example.com", "http://"} {
		_, err := crawler.Analyze(context.Background(), crawler.Options{URL: raw})
		require.Error(t, err, raw)
	}
}
