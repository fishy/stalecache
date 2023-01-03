// Package stalecache provides a cache with optional TTL.
//
// The cache auto reloads after it's stale.
package stalecache

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type cached[T any] struct {
	once   sync.Once
	data   *T
	loaded time.Time
	err    error
}

func (d *cached[T]) load(ctx context.Context, loader Loader[T]) (*T, time.Time, error) {
	d.once.Do(func() {
		d.data, d.err = loader(ctx)
		d.loaded = time.Now()
	})
	return d.data, d.loaded, d.err
}

// Loader defines the callback to load value from external source.
//
// It's called when either the ttl passed, or when validator returned false.
type Loader[T any] func(context.Context) (*T, error)

// Cache defines a cached single value with optional TTL.
type Cache[T any] struct {
	opt opt[T]

	cached atomic.Pointer[cached[T]]
	pool   sync.Pool
}

type opt[T any] struct {
	loader    Loader[T]
	ttl       time.Duration
	validator func(context.Context, *T, time.Time) bool
}

// Option defines Cache options.
type Option[T any] func(*opt[T])

// WithTTL is an Option to set the TTL (time-to-live) for the cache.
//
// Default is 0, means the cache does not expire.
// Set it to positive value will cause it to be re-loaded after expired.
func WithTTL[T any](ttl time.Duration) Option[T] {
	return func(o *opt[T]) {
		o.ttl = ttl
	}
}

// WithValidator is an Option to set a validator to the cache.
//
// Default is nil.
// A non-nil validator will be called (only after the ttl check passed if set),
// and if it returns true the cache will be re-loaded.
//
// An usual use case for validator is to use a faster external source
// (for example, redis) to validate whether the cache is fresh.
func WithValidator[T any](validator func(ctx context.Context, data *T, loaded time.Time) (fresh bool)) Option[T] {
	return func(o *opt[T]) {
		o.validator = validator
	}
}

// New creates a new Cache with loader and options.
func New[T any](loader Loader[T], options ...Option[T]) *Cache[T] {
	o := &opt[T]{
		loader: loader,
	}
	for _, option := range options {
		option(o)
	}
	c := &Cache[T]{
		opt: *o,
		pool: sync.Pool{
			New: func() any {
				return new(cached[T])
			},
		},
	}
	c.cached.Store(c.poolGet())
	return c
}

func (c *Cache[T]) poolGet() *cached[T] {
	return c.pool.Get().(*cached[T])
}

// Load loads the cached value.
//
// If the cached value is stale (or never loaded before),
// the set loader will be called to load it from external source.
//
// If the cached last loader call failed,
// it immediately calls loader again with the same ctx and return the new result
// instead.
// If the cached value is stale but the new loader call failed,
// it returns the cached stale data with error form the new loader call.
//
// In worst case scenario a single Load could call loader twice:
// once another goroutine is causing it to reload (or it's loaded for the first
// time), and the first loader failed so it immediately calls loader again.
//
// A single Cache instance would never have 2 loader calls at the same time.
func (c *Cache[T]) Load(ctx context.Context) (*T, error) {
	curr := c.cached.Load()
	data, loaded, err := curr.load(ctx, c.opt.loader)
	if err == nil {
		fresh := c.opt.ttl <= 0 || loaded.Add(c.opt.ttl).After(time.Now())
		if fresh && c.opt.validator != nil {
			fresh = c.opt.validator(ctx, data, loaded)
		}
		if fresh {
			return data, nil
		}
	}
	// try to re-load new data
	newCached := c.poolGet()
	if !c.cached.CompareAndSwap(curr, newCached) {
		// not swapped, put back to the pool
		c.pool.Put(newCached)
	}
	newData, _, err := c.cached.Load().load(ctx, c.opt.loader)
	if err != nil {
		return data, err
	}
	return newData, nil
}
