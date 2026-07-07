package chatgpt

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmalloc/usagent/internal/config"
)

func TestChatGPTFetchNormalizesWHAMUsage(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"header.`+jwtPayload(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acct-jwt"}}`)+`.sig"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var auth, accountID, userAgent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("authorization")
		accountID = r.Header.Get("chatgpt-account-id")
		userAgent = r.Header.Get("user-agent")
		fmt.Fprint(w, `{
			"plan_type":"pro",
			"rate_limit":{
				"allowed":true,
				"limit_reached":false,
				"primary_window":{"used_percent":25,"limit_window_seconds":18000,"reset_after_seconds":3600},
				"secondary_window":{"used_percent":60,"limit_window_seconds":604800,"reset_after_seconds":86400}
			},
			"additional_rate_limits":[{"display_name":"GPT-5.3-Codex-Spark","primary_window":{"used_percent":10,"limit_window_seconds":18000,"reset_after_seconds":100},"secondary_window":{"used_percent":20,"limit_window_seconds":604800,"reset_after_seconds":200}}],
			"rate_limit_reset_credits":{"available_count":2}
		}`)
	}))
	defer srv.Close()
	res, err := NewWithClient(config.ChatGPTConfig{AuthPath: authPath, EndpointURL: srv.URL, UserAgent: "usagent-test", RefreshMs: 1000, StaleMs: 2000}, srv.Client()).Fetch(context.Background(), time.UnixMilli(1000))
	if err != nil {
		t.Fatal(err)
	}
	if auth == "" || !strings.HasPrefix(auth, "Bearer ") || accountID != "acct-jwt" || userAgent != "usagent-test" {
		t.Fatalf("headers auth=%q account=%q ua=%q", auth, accountID, userAgent)
	}
	if len(res.Items) != 5 {
		t.Fatalf("items=%+v", res.Items)
	}
	byID := map[string]int{}
	for i, item := range res.Items {
		byID[item.ID] = i
	}
	for _, id := range []string{"chatgpt-primary", "chatgpt-secondary", "chatgpt-gpt-5-3-codex-spark-primary", "chatgpt-gpt-5-3-codex-spark-secondary", "chatgpt-rate-limit-reset-credits"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("missing %s in %+v", id, res.Items)
		}
	}
	primary := res.Items[byID["chatgpt-primary"]]
	if primary.Provider != "chatgpt" || primary.Window.ID != "session" || primary.Unit != "percent" || primary.Remaining != 75 || primary.Reset == nil {
		t.Fatalf("primary=%+v", primary)
	}
	weekly := res.Items[byID["chatgpt-secondary"]]
	if weekly.Window.ID != "weekly" || weekly.Remaining != 40 {
		t.Fatalf("weekly=%+v", weekly)
	}
	credits := res.Items[byID["chatgpt-rate-limit-reset-credits"]]
	if credits.Unit != "credits" || credits.Remaining != 2 || credits.Window.ID != "resetCredits" {
		t.Fatalf("credits=%+v", credits)
	}
}

func TestResetCreditListAndConsume(t *testing.T) {
	t.Setenv("CHATGPT_ACCESS_TOKEN", "token")
	t.Setenv("CHATGPT_ACCOUNT_ID", "acct")
	var consumeBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("chatgpt-account-id") != "acct" || r.Header.Get("authorization") != "Bearer token" {
			t.Fatalf("bad auth headers: %v", r.Header)
		}
		switch r.URL.Path {
		case "/credits":
			fmt.Fprint(w, `{"available_count":1,"credits":[{"id":"RateLimitResetCredit_1","status":"available","title":"One free rate limit reset","granted_at":"2026-06-12T01:33:14Z","expires_at":"2026-07-12T01:33:14Z"}]}`)
		case "/consume":
			if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("content-type"), "application/json") {
				t.Fatalf("bad consume request method=%s ct=%s", r.Method, r.Header.Get("content-type"))
			}
			if err := json.NewDecoder(r.Body).Decode(&consumeBody); err != nil {
				t.Fatal(err)
			}
			fmt.Fprint(w, `{"windows_reset":1,"code":"reset","redeemed_at":"2026-06-13T13:12:31Z"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p := NewWithClient(config.ChatGPTConfig{TokenEnv: "CHATGPT_ACCESS_TOKEN", AccountIDEnv: "CHATGPT_ACCOUNT_ID", ResetCreditsEndpointURL: srv.URL + "/credits", ResetConsumeEndpointURL: srv.URL + "/consume"}, srv.Client())
	credits, err := p.ListResetCredits(context.Background(), time.UnixMilli(1000))
	if err != nil {
		t.Fatal(err)
	}
	if credits.AvailableCount != 1 || len(credits.Credits) != 1 || credits.Credits[0].ExpiresAt == "" {
		t.Fatalf("credits=%+v", credits)
	}
	consume, err := p.ConsumeResetCredit(context.Background(), "RateLimitResetCredit_1", "req-1", time.UnixMilli(2000))
	if err != nil {
		t.Fatal(err)
	}
	if consume.WindowsReset != 1 || consume.Code != "reset" || consumeBody["credit_id"] != "RateLimitResetCredit_1" || consumeBody["redeem_request_id"] != "req-1" {
		t.Fatalf("consume=%+v body=%v", consume, consumeBody)
	}
}

func TestChatGPTNon2xxRetryAfter(t *testing.T) {
	t.Setenv("CHATGPT_ACCESS_TOKEN", "token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	res, err := NewWithClient(config.ChatGPTConfig{EndpointURL: srv.URL, TokenEnv: "CHATGPT_ACCESS_TOKEN"}, srv.Client()).Fetch(context.Background(), time.UnixMilli(1000))
	if err == nil || !strings.Contains(err.Error(), "HTTP 429") || !strings.Contains(err.Error(), "slow down") {
		t.Fatalf("err=%v", err)
	}
	if res.RetryAfter != 7*time.Second {
		t.Fatalf("retryAfter=%s", res.RetryAfter)
	}
}

func jwtPayload(jsonPayload string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(jsonPayload))
}
