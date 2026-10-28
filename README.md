# Парсер сайтов (Go)

[![hexlet-check](https://github.com/kicha3007-boop/go-project-316/actions/workflows/hexlet-check.yml/badge.svg)](https://github.com/kicha3007-boop/go-project-316/actions)
[![CI](https://github.com/kicha3007-boop/go-project-316/actions/workflows/ci.yml/badge.svg)](https://github.com/kicha3007-boop/go-project-316/actions/workflows/ci.yml)

Консольный краулер и библиотека: обходит сайт пулом воркеров, проверяет ссылки и статические файлы,
собирает SEO-теги и печатает JSON-отчёт по каждой странице.

## Установка и команды

Нужен Go 1.25 или новее.

```bash
git clone https://github.com/kicha3007-boop/go-project-316.git
cd go-project-316
make install                    # зависимости
make build                      # бинарник bin/hexlet-go-crawler
make test                       # тесты (без сети: транспорт подменяется)
make lint                       # golangci-lint
make run URL=https://example.com ARGS="--depth 2"   # без URL — справка
```

## Использование

```bash
bin/hexlet-go-crawler --help
NAME:
   hexlet-go-crawler - analyze a website structure

USAGE:
   hexlet-go-crawler [global options] <url>

GLOBAL OPTIONS:
   --depth int          crawl depth (default: 10)
   --retries int        number of retries for failed requests (default: 1)
   --delay duration     delay between requests (example: 200ms, 1s) (default: 0s)
   --timeout duration   per-request timeout (default: 15s)
   --rps float          limit requests per second (overrides delay) (default: 0)
   --user-agent string  custom user agent
   --workers int        number of concurrent workers (default: 4)
   --help, -h           show help

bin/hexlet-go-crawler --depth 2 --workers 8 --rps 5 https://example.com > report.json
```

CLI печатает отчёт как есть (с переводом строки в конце). Ошибки сети и коды 4xx/5xx — часть отчёта,
код выхода при них `0`; ненулевой — только при неверных аргументах или прерывании (Ctrl+C).

### Параметры обхода

- **Глубина** (`--depth`, `Options.Depth`) — сколько уровней страниц обходить. Стартовая страница — уровень 0,
  ссылки с неё — уровень 1 и т. д.; обходятся уровни `0 … depth-1`. `--depth 1` — только стартовая страница.
  Обходятся только страницы того же хоста; внешние ссылки проверяются, но не обходятся. Каждый адрес — один раз.
- **Скорость** — `--delay 200ms` (`Options.Delay`) или `--rps 5` (`Options.RPS`, важнее `delay`): минимальный
  интервал между любыми двумя запросами всего процесса (страницы, ссылки, ассеты), а не отдельного воркера.
  Без них скорость не ограничивается.
- **Повторы** — `--retries N` (`Options.Retries`): после первой попытки ещё до N повторов, только для временных
  ошибок — сетевой сбой, `429` и `5xx`. Перед повтором пауза 200 мс, 400 мс, … В отчёт идёт итог последней попытки.
  Коды `4xx` (кроме 429) не повторяются.
- **Таймаут** (`--timeout`) — на одну попытку. Отмена (Ctrl+C) сразу прерывает и ожидание, и повторы;
  отчёт по уже обойдённым страницам остаётся валидным JSON.

## Как библиотека

```go
import (
	"context"
	"net/http"

	"code/crawler"
)

report, err := crawler.Analyze(context.Background(), crawler.Options{
	URL:        "https://example.com",
	Depth:      2,
	HTTPClient: &http.Client{}, // все запросы идут через этот клиент; в тестах — с подменённым транспортом
})
```

`Options`: `URL`, `Depth`, `Retries`, `Delay`, `RPS`, `Timeout`, `UserAgent`, `Concurrency`, `IndentJSON`,
`HTTPClient`, `Clock` (источник времени, в тестах — фейковый). Незаданные поля получают значения по умолчанию
из констант `crawler.Default*`.

## Отчёт

```json
{
  "root_url": "https://example.com",
  "depth": 1,
  "generated_at": "2024-06-01T12:34:56Z",
  "pages": [
    {
      "url": "https://example.com",
      "depth": 0,
      "http_status": 200,
      "status": "ok",
      "seo": {
        "has_title": true,
        "title": "Example title",
        "has_description": true,
        "description": "Example description",
        "has_h1": true
      },
      "broken_links": [
        { "url": "https://example.com/missing", "status_code": 404, "error": "Not Found" }
      ],
      "assets": [
        { "url": "https://example.com/static/logo.png", "type": "image", "status_code": 200, "size_bytes": 12345 }
      ],
      "discovered_at": "2024-06-01T12:34:56Z"
    }
  ]
}
```

| Поле | Значение |
|---|---|
| `root_url`, `depth` | стартовый адрес и глубина обхода |
| `generated_at`, `discovered_at` | время отчёта и загрузки страницы, ISO 8601 (UTC) |
| `pages[]` | страницы по возрастанию глубины, внутри уровня — по адресу |
| `http_status`, `status`, `error` | код ответа (0 — ответа не было), `ok` / `error`, причина ошибки (у `ok` поля нет) |
| `seo` | наличие и текст `<title>`, `<meta name="description">`, наличие `<h1>`; HTML-сущности раскодированы |
| `broken_links[]` | ссылки `<a href>` с ответом 4xx/5xx (`status_code`) или ошибкой сети (`error`); пустые и не-http(s) ссылки не проверяются |
| `assets[]` | картинки, скрипты, стили: `type` (`image`/`script`/`style`), `status_code`, `size_bytes` (из `Content-Length`, без него — по телу), `error` при ошибке |

У недоступной страницы `broken_links` и `assets` — `null`, у доступной — списки (пустые, если нечего показать).
Ссылки проверяются запросом `HEAD` (если сервер его не поддерживает — `GET`); каждый адрес ссылки и ассета
запрашивается один раз за обход, повторные появления берутся из кеша.

## Устройство

- `crawler` — публичный API: `Analyze`, `Options`, `Report`; обход по уровням на структуре `crawler`;
- `internal/fetch` — запросы: таймаут, User-Agent, общий лимит скорости, повторы;
- `internal/htmlparse` — разбор HTML (goquery): SEO, ссылки, ассеты, нормализация адресов;
- `cmd/hexlet-go-crawler` — CLI: только флаги и вызов библиотеки.

Тесты (`crawler/crawler_test.go`) — чёрный ящик: HTML в `crawler/testdata`, сеть подменена транспортом,
время — фейковыми часами; эталонный отчёт — `crawler/testdata/report.json` (`make test-update`).

Проверка Хекслета локально (Docker): образ `hexletprojects/go_crawler_project_ru` содержит тесты, фикстурные сайты
и `docker-compose.yml` с Caddy; код кладётся в `code/`, затем
`docker compose -f docker-compose.yml run --rm app make setup && docker compose -f docker-compose.yml run --rm test`.
