package providers

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"
)

const maxErrorBodyBytes = 512

func HTTPStatusError(prefix string, statusCode int, retryAfter time.Duration, body io.Reader) error {
	parts := []string{fmt.Sprintf("%s returned HTTP %d", prefix, statusCode)}
	if retryAfter > 0 {
		parts = append(parts, fmt.Sprintf("retry after %s", retryAfter.Round(time.Second)))
	}
	if body != nil {
		if snippet := bodySnippet(body); snippet != "" {
			parts = append(parts, snippet)
		}
	}
	return fmt.Errorf(strings.Join(parts, ": "))
}

func bodySnippet(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, maxErrorBodyBytes+1))
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return ""
	}
	truncated := len(b) > maxErrorBodyBytes
	if truncated {
		b = b[:maxErrorBodyBytes]
	}
	s := strings.Join(strings.Fields(string(b)), " ")
	if truncated {
		s += "…"
	}
	return s
}
