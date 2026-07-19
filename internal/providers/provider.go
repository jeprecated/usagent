package providers

import (
	"context"
	"time"

	"github.com/jeprecated/usagent/internal/model"
)

type Result struct {
	Items      []model.QuotaItem
	RetryAfter time.Duration
}

type Provider interface {
	ID() string
	Label() string
	Fetch(ctx context.Context, now time.Time) (Result, error)
}
