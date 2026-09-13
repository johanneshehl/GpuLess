package main

import (
	"context"
	"time"
)

// contextWithTimeout is a background context with a deadline, for work that
// must outlive the request that triggered it.
func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
