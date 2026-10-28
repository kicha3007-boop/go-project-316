package crawler

import (
	"context"
	"net/http"
	"sync"

	"code/internal/fetch"
	"code/internal/htmlparse"
)

// memo выполняет вычисление для ключа один раз, даже если его просят несколько воркеров сразу.
type memo[T any] struct {
	mu      sync.Mutex
	entries map[string]*memoEntry[T]
}

type memoEntry[T any] struct {
	once  sync.Once
	value T
}

func (m *memo[T]) get(key string, compute func() T) T {
	m.mu.Lock()
	if m.entries == nil {
		m.entries = map[string]*memoEntry[T]{}
	}

	entry, ok := m.entries[key]
	if !ok {
		entry = &memoEntry[T]{}
		m.entries[key] = entry
	}
	m.mu.Unlock()

	entry.once.Do(func() { entry.value = compute() })

	return entry.value
}

// linkCheck — итог проверки ссылки.
type linkCheck struct {
	broken     bool
	statusCode int
	err        string
}

// brokenLinks проверяет ссылки страницы и возвращает недоступные; каждый адрес проверяется один раз за обход.
func (c *crawler) brokenLinks(ctx context.Context, links []string) []BrokenLink {
	broken := []BrokenLink{}

	for _, link := range links {
		check := c.links.get(link, func() linkCheck { return c.checkLink(ctx, link) })
		if check.broken {
			broken = append(broken, BrokenLink{URL: link, StatusCode: check.statusCode, Error: check.err})
		}
	}

	return broken
}

func (c *crawler) checkLink(ctx context.Context, link string) linkCheck {
	resp, err := c.head(ctx, link, false)
	switch {
	case err != nil:
		return linkCheck{broken: true, err: err.Error()}
	case resp.StatusCode >= http.StatusBadRequest:
		return linkCheck{broken: true, statusCode: resp.StatusCode, err: http.StatusText(resp.StatusCode)}
	default:
		return linkCheck{statusCode: resp.StatusCode}
	}
}

// checkAssets возвращает статус и размер ассетов страницы; каждый адрес запрашивается один раз за обход.
func (c *crawler) checkAssets(ctx context.Context, refs []htmlparse.Asset) []Asset {
	assets := make([]Asset, 0, len(refs))

	for _, ref := range refs {
		assets = append(assets, c.assets.get(ref.URL, func() Asset { return c.checkAsset(ctx, ref) }))
	}

	return assets
}

func (c *crawler) checkAsset(ctx context.Context, ref htmlparse.Asset) Asset {
	asset := Asset{URL: ref.URL, Type: ref.Type}

	resp, err := c.head(ctx, ref.URL, false)
	if err == nil && resp.StatusCode < http.StatusBadRequest && resp.ContentLength < 0 {
		// Размер не пришёл в заголовке — считаем по телу.
		resp, err = c.fetcher.Fetch(ctx, http.MethodGet, ref.URL, true)
		resp.ContentLength = int64(len(resp.Body))
	}

	switch {
	case err != nil:
		asset.Error = err.Error()
	case resp.StatusCode >= http.StatusBadRequest:
		asset.StatusCode = resp.StatusCode
		asset.Error = http.StatusText(resp.StatusCode)
	default:
		asset.StatusCode = resp.StatusCode
		asset.SizeBytes = resp.ContentLength
	}

	return asset
}

// head делает HEAD-запрос; если сервер HEAD не поддерживает, повторяет GET.
func (c *crawler) head(ctx context.Context, link string, readBody bool) (fetch.Response, error) {
	resp, err := c.fetcher.Fetch(ctx, http.MethodHead, link, readBody)
	if err == nil && (resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusNotImplemented) {
		return c.fetcher.Fetch(ctx, http.MethodGet, link, readBody)
	}

	return resp, err
}
