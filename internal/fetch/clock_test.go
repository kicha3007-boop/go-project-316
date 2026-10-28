package fetch

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// slotClock не двигает время, а запоминает, до какого момента просили подождать.
type slotClock struct {
	mu      sync.Mutex
	now     time.Time
	targets []time.Time
}

func (c *slotClock) Now() time.Time { return c.now }

func (c *slotClock) Sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.targets = append(c.targets, c.now.Add(d))

	return ctx.Err()
}

func TestLimiterSharesIntervalBetweenGoroutines(t *testing.T) {
	clock := &slotClock{now: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	l := &limiter{interval: 100 * time.Millisecond, clock: clock}

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { require.NoError(t, l.wait(context.Background())) })
	}
	wg.Wait()

	// Каждый запрос получил свой слот, слоты идут ровно через интервал.
	slices.SortFunc(clock.targets, time.Time.Compare)
	require.Len(t, clock.targets, 20)

	for i, target := range clock.targets {
		require.Equal(t, clock.now.Add(time.Duration(i)*100*time.Millisecond), target)
	}
}

func TestLimiterWithoutIntervalDoesNotWait(t *testing.T) {
	clock := &slotClock{}
	l := &limiter{clock: clock}

	require.NoError(t, l.wait(context.Background()))
	require.Empty(t, clock.targets)
}

func TestRealClockSleepStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	started := time.Now()
	err := RealClock{}.Sleep(ctx, time.Hour)

	require.ErrorIs(t, err, context.Canceled)
	require.Less(t, time.Since(started), time.Second)
}
