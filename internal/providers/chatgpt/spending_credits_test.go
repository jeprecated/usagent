package chatgpt

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jeprecated/usagent/internal/config"
)

func TestSpendingCreditsOptionalBalance(t *testing.T) {
	t.Setenv("TEST_CHATGPT_TOKEN", "test-token")
	for _, tt := range []struct {
		name    string
		credits string
		visible bool
		balance float64
	}{
		{"string", `{"has_credits":true,"unlimited":false,"balance":"62500"}`, true, 62500},
		{"number", `{"balance":123.45}`, true, 123.45},
		{"fractional string", `{"balance":"123.45"}`, true, 123.45},
		{"zero", `{"has_credits":false,"balance":"0"}`, true, 0},
		{"numeric zero", `{"balance":0}`, true, 0},
		{"missing", "", false, 0},
		{"null credits", `null`, false, 0},
		{"missing balance", `{"has_credits":true}`, false, 0},
		{"null balance", `{"balance":null}`, false, 0},
		{"unlimited", `{"unlimited":true,"balance":"62500"}`, false, 0},
		{"empty balance", `{"balance":""}`, false, 0},
		{"invalid string", `{"balance":"unknown"}`, false, 0},
		{"negative", `{"balance":"-1"}`, false, 0},
		{"boolean", `{"balance":true}`, false, 0},
		{"object", `{"balance":{}}`, false, 0},
		{"array", `[]`, false, 0},
		{"malformed flag", `{"unlimited":"false","balance":"62500"}`, false, 0},
		{"NaN", `{"balance":"NaN"}`, false, 0},
		{"infinity", `{"balance":"Inf"}`, false, 0},
		{"overflow", `{"balance":1e999}`, false, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("unexpected method: %s", r.Method)
				}
				fmt.Fprint(w, `{"rate_limit":{"secondary_window":{"used_percent":100,"limit_window_seconds":604800,"reset_after_seconds":3600}}`)
				if tt.credits != "" {
					fmt.Fprintf(w, `,"credits":%s`, tt.credits)
				}
				fmt.Fprint(w, `}`)
			}))
			defer srv.Close()
			now := time.UnixMilli(1000)
			p := NewWithClient(config.ChatGPTConfig{EndpointURL: srv.URL, TokenEnv: "TEST_CHATGPT_TOKEN", RefreshMs: 2000, StaleMs: 3000}, srv.Client())
			res, exhausted, err := p.FetchForReset(context.Background(), now)
			if err != nil || !exhausted {
				t.Fatalf("optional credits changed weekly exhaustion: exhausted=%v err=%v", exhausted, err)
			}
			if res.Items[0].ID != "chatgpt-secondary" || res.Items[0].Remaining != 0 {
				t.Fatalf("subscription quota lost: %+v", res.Items)
			}
			want := 1
			if tt.visible {
				want++
			}
			if len(res.Items) != want {
				t.Fatalf("items=%+v, want %d", res.Items, want)
			}
			if !tt.visible {
				return
			}
			item := res.Items[1]
			if item.ID != "chatgpt-spending-credits" || item.Remaining != tt.balance || !item.Visible || item.State != "fresh" || item.Estimated {
				t.Fatalf("balance=%+v", item)
			}
			if item.Limit != 0 || item.Used != 0 || item.PercentUsed != 0 || item.Reset != nil || item.Window.ResetAt != nil {
				t.Fatalf("invented credit budget or expiry: %+v", item)
			}
			if item.Refresh == nil || item.Refresh.LastUpdatedAt != 1000 || item.Refresh.NextRefreshAt != 3000 || item.Refresh.StaleAt != 4000 {
				t.Fatalf("refresh=%+v", item.Refresh)
			}
		})
	}
}
