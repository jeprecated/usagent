package ratelimit

import (
	"net/http"
	"testing"
	"time"
)

func TestRetryAfterParsesSecondsHTTPDateAndMilliseconds(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	if got := ParseRetryAfter("7", now); got != 7*time.Second {
		t.Fatalf("seconds=%s", got)
	}
	date := now.Add(9 * time.Second).Format(http.TimeFormat)
	if got := ParseRetryAfter(date, now); got != 9*time.Second {
		t.Fatalf("date=%s", got)
	}
	h := http.Header{"Retry-After-Ms": []string{"2500"}}
	if got := RetryAfter(h, now); got != 2500*time.Millisecond {
		t.Fatalf("ms=%s", got)
	}
}

func TestAnthropicRetryAfterUsesResetHeadersWhenRetryAfterMissing(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	h := http.Header{}
	h.Set("anthropic-ratelimit-requests-remaining", "0")
	h.Set("anthropic-ratelimit-requests-reset", now.Add(11*time.Second).Format(time.RFC3339))
	h.Set("anthropic-ratelimit-tokens-remaining", "3")
	h.Set("anthropic-ratelimit-tokens-reset", now.Add(time.Minute).Format(time.RFC3339))
	if got := AnthropicRetryAfter(h, now); got != 11*time.Second {
		t.Fatalf("got=%s", got)
	}
}

func TestAnthropicRetryAfterUsesGreaterExhaustedReset(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	h := http.Header{}
	h.Set("anthropic-ratelimit-requests-remaining", "0")
	h.Set("anthropic-ratelimit-requests-reset", now.Add(11*time.Second).Format(time.RFC3339))
	h.Set("anthropic-ratelimit-tokens-remaining", "0")
	h.Set("anthropic-ratelimit-tokens-reset", now.Add(31*time.Second).Format(time.RFC3339))
	if got := AnthropicRetryAfter(h, now); got != 31*time.Second {
		t.Fatalf("got=%s", got)
	}
}
