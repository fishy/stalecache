package stalecache_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.yhsif.com/stalecache"
)

func TestCacheLoad(t *testing.T) {
	const (
		ttl   = 10 * time.Millisecond
		sleep = 10 * time.Millisecond
		n     = 5
	)
	loader := func(context.Context) (*int, error) {
		time.Sleep(sleep)
		data := n
		return &data, nil
	}

	cache := stalecache.New(loader, stalecache.WithTTL[int](ttl))
	var wg sync.WaitGroup

	for i := 0; i < 2; i++ {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			before := time.Now()
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()

					data, err := cache.Load(context.Background())
					if err != nil {
						t.Errorf("Load #%d returned error: %v", i, err)
					}
					if *data != n {
						t.Errorf("Load #%d got %d want %d", i, n, *data)
					}
				}(i)
			}
			wg.Wait()
			elapsed := time.Since(before)
			t.Logf("%d Loads took %v", n, elapsed)
			if max := time.Duration(n) * sleep; elapsed > max {
				t.Errorf("%d Loads took more than %v: %v", n, max, elapsed)
			}
		})
		time.Sleep(ttl)
	}
}

func TestCacheLoadFailure(t *testing.T) {
	const (
		ttl   = 10 * time.Millisecond
		sleep = 10 * time.Millisecond
		n     = 5
	)
	wantErr := errors.New("foo")
	var concurrentLoaderCalls atomic.Int64
	loader := func(context.Context) (*int, error) {
		defer concurrentLoaderCalls.Add(-1)
		if calls := concurrentLoaderCalls.Add(1); calls != 1 {
			t.Errorf("Got %d concurrent loader calls, want 1", calls)
		}
		time.Sleep(sleep)
		return nil, wantErr
	}

	cache := stalecache.New(loader, stalecache.WithTTL[int](ttl))
	var wg sync.WaitGroup

	for i := 0; i < 2; i++ {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			before := time.Now()
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()

					data, err := cache.Load(context.Background())
					if err == nil {
						t.Errorf("Load #%d got nil error, want %v", i, wantErr)
					}
					if data != nil {
						t.Errorf("Load #%d got %#v want nil", i, data)
					}
				}(i)
			}
			wg.Wait()
			elapsed := time.Since(before)
			t.Logf("%d Loads took %v", n, elapsed)
			if max := time.Duration(n) * sleep; elapsed > max {
				t.Errorf("%d Loads took more than %v: %v", n, max, elapsed)
			}
		})
		time.Sleep(ttl)
	}
}

func TestCacheValidator(t *testing.T) {
	const sleep = 5 * time.Millisecond
	cache := stalecache.New(
		func(context.Context) (*int, error) {
			time.Sleep(sleep)
			var data int
			return &data, nil
		},
		stalecache.WithTTL[int](time.Second),
		stalecache.WithValidator(func(context.Context, *int, time.Time) (fresh bool) {
			return false
		}),
	)

	for i := 0; i < 10; i++ {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			before := time.Now()
			cache.Load(context.Background())
			if elapsed := time.Since(before); elapsed <= sleep {
				t.Errorf("Load took %v <= %v", elapsed, sleep)
			}
		})
	}
}

func TestCacheUpdate(t *testing.T) {
	const (
		loaded  = "loaded"
		updated = "updated"

		ttl = 10 * time.Millisecond
	)
	cache := stalecache.New(
		func(context.Context) (*string, error) {
			s := loaded
			return &s, nil
		},
		stalecache.WithTTL[string](ttl),
	)

	checkLoaded := func(t *testing.T, want string) {
		t.Helper()
		data, err := cache.Load(context.Background())
		if err != nil {
			t.Fatalf("Load got error: %v", err)
		}
		if *data != want {
			t.Errorf("Load got %q, want %q", *data, want)
		}
	}

	t.Run("load", func(t *testing.T) {
		checkLoaded(t, loaded)
	})

	t.Run("update", func(t *testing.T) {
		time.Sleep(ttl / 2)
		s := updated
		cache.Update(&s)
		checkLoaded(t, updated)
	})

	t.Run("not-stale", func(t *testing.T) {
		time.Sleep(ttl / 2)
		checkLoaded(t, updated)
	})

	t.Run("stale", func(t *testing.T) {
		time.Sleep(ttl / 2)
		checkLoaded(t, loaded)
	})
}
