package crawler

import (
	"net/http"
	"time"

	"code/internal/fetch"
)

// Значения по умолчанию — единственное место, где они заданы; CLI берёт их отсюда же.
const (
	DefaultDepth        = 10
	DefaultRetries      = 1
	DefaultTimeout      = 15 * time.Second
	DefaultConcurrency  = 4
	DefaultUserAgent    = "hexlet-go-crawler/1.0"
	DefaultRetryBackoff = 200 * time.Millisecond
)

// Clock — источник времени и ожидания (для тестов без реальных пауз).
type Clock = fetch.Clock

// Options — параметры обхода.
type Options struct {
	URL         string        // стартовая страница
	Depth       int           // сколько уровней страниц обходить: 1 — только стартовая; 0 — DefaultDepth
	Retries     int           // повторов после первой попытки при сетевой ошибке, 429 и 5xx
	Delay       time.Duration // пауза между любыми двумя запросами процесса
	RPS         float64       // лимит запросов в секунду на весь процесс; если больше 0, важнее Delay
	Timeout     time.Duration // таймаут одной попытки; 0 — DefaultTimeout
	UserAgent   string        // заголовок User-Agent; пусто — DefaultUserAgent
	Concurrency int           // число воркеров; 0 — DefaultConcurrency
	IndentJSON  bool          // отчёт с отступами; на содержимое не влияет
	HTTPClient  *http.Client  // клиент для всех запросов; nil — &http.Client{}
	Clock       Clock         // источник времени; nil — настоящее время
}

// withDefaults подставляет значения по умолчанию в незаданные поля.
func (o Options) withDefaults() Options {
	if o.Depth <= 0 {
		o.Depth = DefaultDepth
	}

	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}

	if o.Concurrency <= 0 {
		o.Concurrency = DefaultConcurrency
	}

	if o.UserAgent == "" {
		o.UserAgent = DefaultUserAgent
	}

	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{}
	}

	if o.Clock == nil {
		o.Clock = fetch.RealClock{}
	}

	o.Retries = max(o.Retries, 0)

	return o
}

// interval — минимальный промежуток между запросами: RPS важнее Delay.
func (o Options) interval() time.Duration {
	if o.RPS > 0 {
		return time.Duration(float64(time.Second) / o.RPS)
	}

	return o.Delay
}
