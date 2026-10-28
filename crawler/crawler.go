// Package crawler обходит сайт и собирает JSON-отчёт: SEO-теги, битые ссылки и ассеты каждой страницы.
package crawler

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	"code/internal/fetch"
	"code/internal/htmlparse"
)

// Analyze обходит сайт с opts.URL и возвращает JSON-отчёт с переводом строки в конце.
// Сетевые ошибки страниц попадают в отчёт, а не в ошибку. При отмене ctx возвращается отчёт
// по уже обойдённым страницам вместе с ошибкой контекста.
func Analyze(ctx context.Context, opts Options) ([]byte, error) {
	opts = opts.withDefaults()

	root, err := parseRoot(opts.URL)
	if err != nil {
		return nil, err
	}

	report := newCrawler(opts).run(ctx, root)

	data, err := encode(report, opts.IndentJSON)
	if err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return data, fmt.Errorf("crawl interrupted: %w", err)
	}

	return data, nil
}

// crawler хранит состояние обхода: настройки, клиент, посещённые адреса и кеш проверок.
type crawler struct {
	opts    Options
	fetcher *fetch.Fetcher
	host    string

	mu      sync.Mutex
	visited map[string]bool

	links  memo[linkCheck]
	assets memo[Asset]
}

func newCrawler(opts Options) *crawler {
	return &crawler{
		opts: opts,
		fetcher: fetch.New(fetch.Config{
			Client:       opts.HTTPClient,
			UserAgent:    opts.UserAgent,
			Timeout:      opts.Timeout,
			Retries:      opts.Retries,
			RetryBackoff: DefaultRetryBackoff,
			Interval:     opts.interval(),
			Clock:        opts.Clock,
		}),
		visited: map[string]bool{},
	}
}

// run обходит сайт по уровням: уровень depth обрабатывается воркерами, его внутренние ссылки
// становятся следующим уровнем, пока не исчерпана глубина.
func (c *crawler) run(ctx context.Context, root *url.URL) Report {
	c.host = root.Hostname()
	report := Report{RootURL: c.opts.URL, Depth: c.opts.Depth, GeneratedAt: c.now(), Pages: []Page{}}

	start := htmlparse.Normalize(root)
	c.markVisited(start)

	frontier := []string{start}
	for depth := 0; depth < c.opts.Depth && len(frontier) > 0 && ctx.Err() == nil; depth++ {
		var next []string

		for _, result := range c.crawlLevel(ctx, frontier, depth) {
			report.Pages = append(report.Pages, result.page)

			if depth+1 < c.opts.Depth {
				next = append(next, c.unvisited(result.internalLinks)...)
			}
		}

		frontier = next
	}

	slices.SortFunc(report.Pages, func(a, b Page) int {
		return cmp.Or(cmp.Compare(a.Depth, b.Depth), cmp.Compare(a.URL, b.URL))
	})

	return report
}

type pageResult struct {
	page          Page
	internalLinks []string
	done          bool
}

// crawlLevel обходит страницы одного уровня не более чем Concurrency воркерами.
// Страницы, до которых не дошли из-за отмены, в результат не попадают.
func (c *crawler) crawlLevel(ctx context.Context, urls []string, depth int) []pageResult {
	results := make([]pageResult, len(urls))
	jobs := make(chan int)

	var wg sync.WaitGroup
	for range min(c.opts.Concurrency, len(urls)) {
		wg.Go(func() {
			for i := range jobs {
				page, links := c.crawlPage(ctx, urls[i], depth)
				results[i] = pageResult{page: page, internalLinks: links, done: true}
			}
		})
	}

	for i := range urls {
		if ctx.Err() != nil {
			break
		}
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	return slices.DeleteFunc(results, func(r pageResult) bool { return !r.done })
}

// crawlPage загружает страницу и собирает по ней отчёт; возвращает и внутренние ссылки для обхода.
func (c *crawler) crawlPage(ctx context.Context, pageURL string, depth int) (Page, []string) {
	page := Page{URL: pageURL, Depth: depth, Status: StatusError, DiscoveredAt: c.now()}

	resp, err := c.fetcher.Fetch(ctx, http.MethodGet, pageURL, true)
	if err != nil {
		page.Error = err.Error()

		return page, nil
	}

	page.HTTPStatus = resp.StatusCode
	if resp.StatusCode >= http.StatusBadRequest {
		page.Error = http.StatusText(resp.StatusCode)

		return page, nil
	}

	base, err := url.Parse(pageURL)
	if err != nil {
		page.Error = err.Error()

		return page, nil
	}

	doc, err := htmlparse.Parse(bytes.NewReader(resp.Body), base)
	if err != nil {
		page.Error = err.Error()

		return page, nil
	}

	page.Status = StatusOK
	page.SEO = SEO(doc.SEO)
	page.BrokenLinks = c.brokenLinks(ctx, doc.Links)
	page.Assets = c.checkAssets(ctx, doc.Assets)

	return page, c.internal(doc.Links)
}

// internal оставляет ссылки на тот же хост: сравниваются разобранные адреса, а не подстроки.
func (c *crawler) internal(links []string) []string {
	var result []string

	for _, link := range links {
		if u, err := url.Parse(link); err == nil && u.Hostname() == c.host {
			result = append(result, link)
		}
	}

	return result
}

// unvisited отмечает адреса посещёнными и возвращает те, что встретились впервые.
func (c *crawler) unvisited(links []string) []string {
	var result []string

	for _, link := range links {
		if c.markVisited(link) {
			result = append(result, link)
		}
	}

	return result
}

func (c *crawler) markVisited(link string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.visited[link] {
		return false
	}

	c.visited[link] = true

	return true
}

func (c *crawler) now() time.Time {
	return c.opts.Clock.Now().UTC()
}

func parseRoot(raw string) (*url.URL, error) {
	root, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid url %q: %w", raw, err)
	}

	if root.Scheme != "http" && root.Scheme != "https" || root.Host == "" {
		return nil, fmt.Errorf("invalid url %q: %w", raw, errUnsupportedURL)
	}

	return root, nil
}

var errUnsupportedURL = errors.New("expected absolute http(s) address")

func encode(report Report, indent bool) ([]byte, error) {
	var (
		data []byte
		err  error
	)

	if indent {
		data, err = json.MarshalIndent(report, "", "  ")
	} else {
		data, err = json.Marshal(report)
	}

	if err != nil {
		return nil, fmt.Errorf("encode report: %w", err)
	}

	return append(data, '\n'), nil
}
