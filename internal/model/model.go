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
	Tier          string        `json:"tier,omitempty"`
	Tags          []string      `json:"tags,omitempty"`
	LastUpdatedAt *int64        `json:"lastUpdatedAt,omitempty"`
	Error         *ItemError    `json:"error,omitempty"`
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
	ID           string     `json:"id"`
	Provider     string     `json:"provider"`
	Label        string     `json:"label"`
	Window       Window     `json:"window"`
	Unit         string     `json:"unit"`
	Limit        float64    `json:"limit"`
	Used         float64    `json:"used"`
	Remaining    float64    `json:"remaining"`
	PercentUsed  float64    `json:"percentUsed"`
	State        string     `json:"state"`
	Severity     string     `json:"severity"`
	Visible      bool       `json:"visible"`
	ProviderTier string     `json:"providerTier,omitempty"`
	ProviderTags []string   `json:"providerTags,omitempty"`
	ModelTier    string     `json:"modelTier,omitempty"`
	ModelTags    []string   `json:"modelTags,omitempty"`
	Refresh      *Refresh   `json:"refresh,omitempty"`
	Reset        *Reset     `json:"reset,omitempty"`
	Error        *ItemError `json:"error,omitempty"`
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

type ChatGPTResetCredit struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Title     string `json:"title,omitempty"`
	GrantedAt string `json:"grantedAt,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

type ChatGPTResetCreditsResponse struct {
	Provider       string                 `json:"provider"`
	AvailableCount int                    `json:"availableCount"`
	Credits        []ChatGPTResetCredit   `json:"credits"`
	FetchedAt      int64                  `json:"fetchedAt"`
	Raw            map[string]interface{} `json:"raw,omitempty"`
}

type ChatGPTResetConsumeResponse struct {
	Provider        string                 `json:"provider"`
	CreditID        string                 `json:"creditId"`
	RedeemRequestID string                 `json:"redeemRequestId"`
	ConsumedAt      int64                  `json:"consumedAt"`
	WindowsReset    int                    `json:"windowsReset,omitempty"`
	Code            string                 `json:"code,omitempty"`
	RedeemedAt      string                 `json:"redeemedAt,omitempty"`
	Raw             map[string]interface{} `json:"raw,omitempty"`
}
