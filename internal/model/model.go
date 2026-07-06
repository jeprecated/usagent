package model

type ProviderState string

const (
	ProviderStateFresh ProviderState = "fresh"
	ProviderStateStale ProviderState = "stale"
	ProviderStateError ProviderState = "error"
)

type Provider struct {
	ID            string        `json:"id"`
	Label         string        `json:"label"`
	State         ProviderState `json:"state"`
	Source        string        `json:"source"`
	LastUpdatedAt *int64        `json:"lastUpdatedAt,omitempty"`
}

type Window struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	ResetAt *int64 `json:"resetAt,omitempty"`
}

type Refresh struct {
	LastUpdatedAt int64  `json:"lastUpdatedAt"`
	Source        string `json:"source"`
	NextRefreshAt int64  `json:"nextRefreshAt"`
	StaleAt       int64  `json:"staleAt"`
}

type Reset struct {
	ResetAt       int64  `json:"resetAt"`
	ResetWindowID string `json:"resetWindowId"`
	Source        string `json:"source"`
}

type ItemError struct {
	Provider       string `json:"provider"`
	Code           string `json:"code"`
	Message        string `json:"message"`
	LastOccurredAt int64  `json:"lastOccurredAt"`
	Recoverable    bool   `json:"recoverable"`
}

type QuotaItem struct {
	ID          string     `json:"id"`
	Provider    string     `json:"provider"`
	Label       string     `json:"label"`
	Window      Window     `json:"window"`
	Unit        string     `json:"unit"`
	Limit       float64    `json:"limit"`
	Used        float64    `json:"used"`
	Remaining   float64    `json:"remaining"`
	PercentUsed float64    `json:"percentUsed"`
	State       string     `json:"state"`
	Severity    string     `json:"severity"`
	Visible     bool       `json:"visible"`
	Refresh     *Refresh   `json:"refresh,omitempty"`
	Reset       *Reset     `json:"reset,omitempty"`
	Error       *ItemError `json:"error,omitempty"`
}

type Usage struct {
	SchemaVersion int         `json:"schemaVersion"`
	Service       string      `json:"service"`
	GeneratedAt   int64       `json:"generatedAt"`
	StartedAt     int64       `json:"startedAt"`
	Stale         bool        `json:"stale"`
	Providers     []Provider  `json:"providers"`
	QuotaItems    []QuotaItem `json:"quotaItems"`
}

type ProvidersResponse struct {
	Providers []Provider `json:"providers"`
}
