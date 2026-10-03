package claude

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jeprecated/usagent/internal/config"
)

func TestResetCreditsClientSurface(t *testing.T) {
	credPath := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(credPath, []byte(`{"claudeAiOauth":{"accessToken":"test-token"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Query().Get("cedar_ember") != "1" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("User-Agent") == "claude-cli/2.1.287 (external, cli)" {
			fmt.Fprint(w, `{"limits":[{"kind":"session","percent":10}],"cedar_ember":{"eligible":true,"grants":[{"id":"launch","resets_left":1,"usable_now":true,"ends_at":"2026-10-22T16:00:00Z"}]}}`)
		} else {
			fmt.Fprint(w, `{"limits":[{"kind":"session","percent":10}],"cedar_ember":{"eligible":false,"ineligible_reason":"surface","grants":[]}}`)
		}
	}))
	defer srv.Close()

	for _, tt := range []struct {
		name, userAgent string
		rejected        bool
	}{
		{"default", "", false},
		{"legacy override", "claude-code/2.0", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := New(config.ClaudeOAuthConfig{CredentialsPath: credPath, EndpointURL: srv.URL, UserAgent: tt.userAgent})
			now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
			result, err := p.Fetch(context.Background(), now)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Items) == 0 || result.Items[0].Remaining != 90 {
				t.Fatalf("normal usage lost: %+v", result)
			}
			wantItems := 2
			if tt.rejected {
				wantItems = 1
			}
			if len(result.Items) != wantItems {
				t.Fatalf("items=%+v", result.Items)
			}
			if !tt.rejected && result.Items[1].Remaining != 1 {
				t.Fatalf("reset balance=%+v", result.Items[1])
			}
			credits, err := p.ListResetCredits(context.Background(), now)
			if tt.rejected {
				if err == nil || !strings.Contains(err.Error(), "client surface") {
					t.Fatalf("expected surface error, got credits=%+v err=%v", credits, err)
				}
			} else if err != nil || !credits.Eligible || credits.AvailableCount != 1 {
				t.Fatalf("credits=%+v err=%v", credits, err)
			}
		})
	}
}

func TestNormalizeResetCreditsGenuineZero(t *testing.T) {
	for _, block := range []*cedarEmber{
		{Eligible: true},
		{Eligible: false, IneligibleReason: "plan"},
	} {
		items := Normalize(usagePayload{CedarEmber: block}, time.Now(), 1000, 2000)
		if len(items) != 1 || items[0].ID != "claude-code-rate-limit-reset-credits" || items[0].Remaining != 0 {
			t.Fatalf("genuine zero should be reported: %+v", items)
		}
	}
	if items := Normalize(usagePayload{}, time.Now(), 1000, 2000); len(items) != 0 {
		t.Fatalf("missing block must not fabricate a balance: %+v", items)
	}
}
