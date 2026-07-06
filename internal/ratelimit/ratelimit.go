package ratelimit

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const minDelay = time.Second

func RetryAfter(headers http.Header, now time.Time) time.Duration {
	if d := parseRetryAfterMS(headers.Get("retry-after-ms")); d > 0 {
		return atLeastMin(d)
	}
	return ParseRetryAfter(headers.Get("retry-after"), now)
}

func AnthropicRetryAfter(headers http.Header, now time.Time) time.Duration {
	if d := RetryAfter(headers, now); d > 0 {
		return d
	}
	var waits []time.Duration
	if isZero(headers.Get("anthropic-ratelimit-requests-remaining")) {
		if d := untilReset(headers.Get("anthropic-ratelimit-requests-reset"), now); d > 0 {
			waits = append(waits, d)
		}
	}
	if isZero(headers.Get("anthropic-ratelimit-tokens-remaining")) {
		if d := untilReset(headers.Get("anthropic-ratelimit-tokens-reset"), now); d > 0 {
			waits = append(waits, d)
		}
	}
	return maxDuration(waits...)
}

func ParseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil {
		return atLeastMin(time.Duration(secs * float64(time.Second)))
	}
	if t, err := http.ParseTime(v); err == nil {
		return atLeastMin(t.Sub(now))
	}
	return 0
}

func parseRetryAfterMS(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	ms, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0
	}
	return time.Duration(ms * float64(time.Millisecond))
}

func untilReset(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return atLeastMin(t.Sub(now))
	}
	if t, err := http.ParseTime(v); err == nil {
		return atLeastMin(t.Sub(now))
	}
	if unix, err := strconv.ParseFloat(v, 64); err == nil && unix > 0 {
		if unix > 1e12 {
			return atLeastMin(time.UnixMilli(int64(unix)).Sub(now))
		}
		return atLeastMin(time.Unix(int64(unix), 0).Sub(now))
	}
	return 0
}

func isZero(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	n, err := strconv.ParseFloat(v, 64)
	return err == nil && math.Abs(n) < 0.000001
}

func maxDuration(ds ...time.Duration) time.Duration {
	var out time.Duration
	for _, d := range ds {
		if d > out {
			out = d
		}
	}
	return out
}

func atLeastMin(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	if d < minDelay {
		return minDelay
	}
	return d
}
