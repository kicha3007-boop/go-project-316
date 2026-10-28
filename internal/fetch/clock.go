package fetch

import (
	"context"
	"sync"
	"time"
)

// Clock — источник времени и ожидания; в тестах подменяется, чтобы не ждать по-настоящему.
type Clock interface {
	Now() time.Time
	// Sleep ждёт d или отмены ctx, что наступит раньше; при отмене возвращает ошибку контекста.
	Sleep(ctx context.Context, d time.Duration) error
}

// RealClock — настоящее время.
type RealClock struct{}

// Now возвращает текущее время.
func (RealClock) Now() time.Time { return time.Now() }

// Sleep ждёт d, но сразу выходит при отмене ctx.
func (RealClock) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// limiter выдерживает интервал между запросами всего процесса, а не отдельного воркера.
type limiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
	clock    Clock
}

// wait резервирует ближайший свободный слот и ждёт его.
func (l *limiter) wait(ctx context.Context) error {
	if l.interval <= 0 {
		return ctx.Err()
	}

	l.mu.Lock()
	now := l.clock.Now()

	slot := l.next
	if slot.Before(now) {
		slot = now
	}

	l.next = slot.Add(l.interval)
	l.mu.Unlock()

	return l.clock.Sleep(ctx, slot.Sub(now))
}
