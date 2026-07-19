package noop

import (
	"context"
	"time"

	"github.com/jeprecated/usagent/internal/model"
	"github.com/jeprecated/usagent/internal/providers"
)

type Provider struct {
	id    string
	label string
}

func New(id, label string) Provider { return Provider{id: id, label: label} }
func (p Provider) ID() string       { return p.id }
func (p Provider) Label() string    { return p.label }
func (p Provider) Fetch(context.Context, time.Time) (providers.Result, error) {
	return providers.Result{Items: []model.QuotaItem{}}, nil
}
