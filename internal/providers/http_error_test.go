package providers

import (
	"strings"
	"testing"
	"time"
)

func TestHTTPStatusErrorIncludesRetryAndBoundedBodySnippet(t *testing.T) {
	err := HTTPStatusError("openai costs", 429, 7*time.Second, strings.NewReader("  {\n  \"error\": \"slow down\"\n}\n"))
	got := err.Error()
	for _, want := range []string{"openai costs returned HTTP 429", "retry after 7s", `"error": "slow down"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("error %q missing %q", got, want)
		}
	}
}
