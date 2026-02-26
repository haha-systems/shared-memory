package ttl

import (
	"context"
	"time"

	"github.com/charmbracelet/log"
)

// Expirer represents cleanup behavior needed by the worker.
type Expirer interface {
	ExpireShort(ctx context.Context) (int64, error)
}

// Start launches a periodic TTL cleanup worker.
func Start(ctx context.Context, logger *log.Logger, interval time.Duration, expirer Expirer) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := expirer.ExpireShort(ctx)
			if err != nil {
				logger.Warn("ttl cleanup failed", "error", err)
				continue
			}
			if n > 0 {
				logger.Info("ttl cleanup removed expired short memories", "count", n)
			}
		}
	}
}
