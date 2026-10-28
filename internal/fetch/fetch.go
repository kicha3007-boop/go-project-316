// Package fetch выполняет HTTP-запросы краулера: таймаут, User-Agent, общий для процесса лимит скорости
// и повторы при временных ошибках.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Config — настройки Fetcher.
type Config struct {
	Client       *http.Client
	UserAgent    string
	Timeout      time.Duration // на одну попытку
	Retries      int           // повторов после первой попытки
	RetryBackoff time.Duration // пауза перед первым повтором, дальше растёт линейно
	Interval     time.Duration // минимальный интервал между любыми двумя запросами процесса
	Clock        Clock
}

// Response — итог запроса.
type Response struct {
	StatusCode    int
	Body          []byte // только если тело просили прочитать
	ContentLength int64  // из заголовка; -1 — сервер его не прислал
}

// Fetcher выполняет запросы по Config.
type Fetcher struct {
	cfg     Config
	limiter *limiter
}

// New создаёт Fetcher.
func New(cfg Config) *Fetcher {
	return &Fetcher{cfg: cfg, limiter: &limiter{interval: cfg.Interval, clock: cfg.Clock}}
}

// Fetch выполняет запрос и повторяет его при временной ошибке (сеть, 429, 5xx) не больше Retries раз.
// Возвращается результат последней попытки.
func (f *Fetcher) Fetch(ctx context.Context, method, url string, readBody bool) (Response, error) {
	for attempt := 0; ; attempt++ {
		resp, err := f.once(ctx, method, url, readBody)
		if !temporary(resp, err) || attempt >= f.cfg.Retries || ctx.Err() != nil {
			return resp, err
		}

		if waitErr := f.cfg.Clock.Sleep(ctx, f.cfg.RetryBackoff*time.Duration(attempt+1)); waitErr != nil {
			return resp, err
		}
	}
}

// once — одна попытка с учётом лимита скорости и таймаута. Тело закрывается на всех путях.
func (f *Fetcher) once(ctx context.Context, method, url string, readBody bool) (Response, error) {
	if err := f.limiter.wait(ctx); err != nil {
		return Response{}, fmt.Errorf("%s %q: %w", method, url, err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, f.cfg.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, method, url, nil)
	if err != nil {
		return Response{}, fmt.Errorf("build request: %w", err)
	}

	if f.cfg.UserAgent != "" {
		req.Header.Set("User-Agent", f.cfg.UserAgent)
	}

	// Ошибка клиента уже содержит метод и адрес (Get "http://…": …) и идёт в отчёт как есть.
	resp, err := f.cfg.Client.Do(req)
	if err != nil {
		return Response{}, err
	}

	result := Response{StatusCode: resp.StatusCode, ContentLength: resp.ContentLength}
	if readBody {
		result.Body, err = io.ReadAll(resp.Body)
	}

	closeErr := resp.Body.Close()

	switch {
	case err != nil:
		return result, fmt.Errorf("read body of %s: %w", url, err)
	case closeErr != nil:
		return result, fmt.Errorf("close body of %s: %w", url, closeErr)
	default:
		return result, nil
	}
}

// temporary — ошибка, которую имеет смысл повторить: сетевой сбой, 429 или 5xx. Отмену не повторяем.
func temporary(resp Response, err error) bool {
	if err != nil {
		return !errors.Is(err, context.Canceled)
	}

	return resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError
}
