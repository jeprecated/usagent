package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jeprecated/usagent/internal/config"
	"github.com/jeprecated/usagent/internal/model"
)

type resetFixture struct {
	t            *testing.T
	app          *App
	now          time.Time
	usage        atomic.Value
	credits      atomic.Value
	consumeBody  atomic.Value
	usageCode    atomic.Int32
	listCode     atomic.Int32
	consumeCode  atomic.Int32
	dropResponse atomic.Bool
	gets         atomic.Int32
	lists        atomic.Int32
	posts        atomic.Int32
	listHook     func()
	consumeHook  func()
}

func newResetFixture(t *testing.T) *resetFixture {
	t.Helper()
	f := &resetFixture{t: t, now: time.Now()}
	f.usage.Store(weeklyUsage(100))
	f.credits.Store(`{"available_count":5,"credits":[
 {"id":"late","status":"available","expires_at":"2099-02-01T00:00:00Z"},
 {"id":"expired","status":"available","expires_at":"2000-01-01T00:00:00Z"},
 {"id":"spent","status":"redeemed","expires_at":"2098-01-01T00:00:00Z"},
 {"id":"unknown-expiry","status":"available"},
 {"id":"early","status":"available","expires_at":"2099-01-01T00:00:00Z"}]}`)
	f.consumeBody.Store(`{"windows_reset":2,"code":"reset","redeemed_at":"2026-06-13T13:12:31Z"}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fake-token" || r.Header.Get("ChatGPT-Account-Id") != "account-a" {
			t.Errorf("unexpected credentials on %s", r.URL.Path)
			http.Error(w, "wrong account", 401)
			return
		}
		switch r.URL.Path {
		case "/usage":
			f.gets.Add(1)
			if code := f.usageCode.Load(); code != 0 {
				w.WriteHeader(int(code))
			}
			fmt.Fprint(w, f.usage.Load().(string))
		case "/credits":
			f.lists.Add(1)
			if f.listHook != nil {
				f.listHook()
			}
			if code := f.listCode.Load(); code != 0 {
				w.WriteHeader(int(code))
			}
			fmt.Fprint(w, f.credits.Load().(string))
		case "/consume":
			f.posts.Add(1)
			if r.Method != "POST" {
				t.Error("consume must POST")
			}
			var req map[string]string
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			// This assertion runs at the external side-effect boundary: intent MUST
			// already be durable, with exactly the ID sent on the wire.
			saved, err := os.ReadFile(f.app.resetOncePath())
			var state model.ChatGPTResetOnce
			if err != nil || json.Unmarshal(saved, &state) != nil || state.Status != "unknown" || state.CreditID != req["credit_id"] || state.RedeemRequestID != req["redeem_request_id"] || state.RedeemRequestID == "" {
				t.Errorf("redemption before durable claim: %s err=%v req=%v", saved, err, req)
			}
			if f.consumeHook != nil {
				f.consumeHook()
			}
			if f.dropResponse.Load() {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = conn.Close()
				return
			}
			if code := f.consumeCode.Load(); code != 0 {
				w.WriteHeader(int(code))
			}
			fmt.Fprint(w, f.consumeBody.Load().(string))
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	cfg := config.Default()
	cfg.Server.StatePath = filepath.Join(t.TempDir(), "snapshot.json")
	cfg.Providers.ChatGPT = config.ChatGPTConfig{Enabled: true, AuthPath: filepath.Join(t.TempDir(), "auth.json"), EndpointURL: srv.URL + "/usage", ResetCreditsEndpointURL: srv.URL + "/credits", ResetConsumeEndpointURL: srv.URL + "/consume", RefreshMs: 1000, StaleMs: 2000}
	if err := os.WriteFile(cfg.Providers.ChatGPT.AuthPath, []byte(`{"tokens":{"access_token":"fake-token","account_id":"account-a"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	f.app = New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	f.app.EnableResetOnce()
	return f
}

func weeklyUsage(used float64) string {
	return fmt.Sprintf(`{"rate_limit":{"primary_window":{"used_percent":%g,"limit_window_seconds":604800,"reset_after_seconds":86400}}}`, used)
}
func (f *resetFixture) arm() model.ChatGPTResetOnce {
	f.t.Helper()
	s, err := f.app.ArmChatGPTResetOnce(f.now)
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}
func (f *resetFixture) refresh() {
	f.t.Helper()
	f.app.RefreshOne(context.Background(), f.app.chatGPTProvider(), f.now)
}
func (f *resetFixture) state() model.ChatGPTResetOnce {
	f.t.Helper()
	return f.app.ChatGPTResetOnceStatus()
}
func (f *resetFixture) restart(daemon bool) {
	f.app = New(f.app.Cfg, f.app.Logger)
	if err := f.app.LoadState(); err != nil {
		f.t.Fatal(err)
	}
	if daemon {
		f.app.EnableResetOnce()
	}
}

func TestResetOnceConsumesExactlyOneEarliestExpiringCredit(t *testing.T) {
	f := newResetFixture(t)
	first := f.arm()
	second := f.arm()
	if first != second {
		t.Fatalf("repeated arm changed authorization: %+v -> %+v", first, second)
	}
	if f.posts.Load() != 0 || f.gets.Load() != 0 {
		t.Fatal("arming contacted provider")
	}
	f.refresh()
	if f.posts.Load() != 1 || f.state().Status != "consumed" || f.state().CreditID != "early" {
		t.Fatalf("posts=%d state=%+v", f.posts.Load(), f.state())
	}
	for i := 0; i < 3; i++ {
		f.refresh()
	}
	f.restart(true)
	f.refresh()
	if f.posts.Load() != 1 {
		t.Fatalf("reused one-shot authorization: %d", f.posts.Load())
	}
	if f.gets.Load() < 3 {
		t.Fatal("usage not refreshed after redemption")
	}
}

func TestResetOnceDoesNotSpendWithoutFreshWeeklyExhaustion(t *testing.T) {
	for name, usage := range map[string]string{
		"fractional": weeklyUsage(99.9), "remaining": weeklyUsage(97), "missing": "{}", "null": "null",
		"invalid-percent": weeklyUsage(101), "negative-percent": weeklyUsage(-1),
		"session-only":     `{"rate_limit":{"primary_window":{"used_percent":100,"limit_window_seconds":18000,"reset_after_seconds":1000}}}`,
		"model-only":       `{"additional_rate_limits":[{"primary_window":{"used_percent":100,"limit_window_seconds":604800,"reset_after_seconds":1000}}]}`,
		"unknown-duration": `{"rate_limit":{"primary_window":{"used_percent":100,"reset_after_seconds":1000}}}`,
		"already-reset":    `{"rate_limit":{"primary_window":{"used_percent":100,"limit_window_seconds":604800,"reset_after_seconds":0}}}`,
		"not-seven-days":   `{"rate_limit":{"primary_window":{"used_percent":100,"limit_window_seconds":518400,"reset_after_seconds":1000}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newResetFixture(t)
			f.arm()
			f.usage.Store(usage)
			f.refresh()
			if f.posts.Load() != 0 || f.lists.Load() != 0 {
				t.Fatalf("spent without exhausted weekly: %d", f.posts.Load())
			}
		})
	}
	t.Run("unarmed", func(t *testing.T) {
		f := newResetFixture(t)
		f.refresh()
		if f.posts.Load() != 0 || f.lists.Load() != 0 {
			t.Fatal("unarmed side effect")
		}
	})
	t.Run("failed-fetch-with-cached-zero", func(t *testing.T) {
		f := newResetFixture(t)
		f.refresh()
		f.arm()
		f.usageCode.Store(503)
		f.refresh()
		if f.posts.Load() != 0 || f.lists.Load() != 0 {
			t.Fatal("used stale cache")
		}
	})
	t.Run("local-refresh", func(t *testing.T) {
		f := newResetFixture(t)
		f.arm()
		f.restart(false)
		f.refresh()
		if f.posts.Load() != 0 {
			t.Fatal("local CLI consumed credit")
		}
	})
	t.Run("reset-during-list", func(t *testing.T) {
		f := newResetFixture(t)
		f.arm()
		f.listHook = func() { f.usage.Store(weeklyUsage(0)) }
		f.refresh()
		if f.posts.Load() != 0 {
			t.Fatal("spent after natural/external reset")
		}
	})
}

func TestResetOnceSecondaryWeeklyAndRestart(t *testing.T) {
	f := newResetFixture(t)
	f.arm()
	f.restart(true)
	f.usage.Store(`{"rate_limit":{"primary_window":{"used_percent":20,"limit_window_seconds":18000,"reset_after_seconds":3600},"secondary_window":{"used_percent":100,"limit_window_seconds":604800,"reset_after_seconds":86400}}}`)
	f.refresh()
	if f.posts.Load() != 1 {
		t.Fatalf("posts=%d", f.posts.Load())
	}
}

func TestResetOnceCancellationAndAccountChange(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		f := newResetFixture(t)
		f.arm()
		if _, err := f.app.CancelChatGPTResetOnce(false, f.now); err != nil {
			t.Fatal(err)
		}
		f.restart(true)
		f.refresh()
		if f.posts.Load() != 0 || f.state().Status != "cancelled" {
			t.Fatalf("state=%+v", f.state())
		}
	})
	t.Run("account-change", func(t *testing.T) {
		f := newResetFixture(t)
		f.arm()
		if err := os.WriteFile(f.app.Cfg.Providers.ChatGPT.AuthPath, []byte(`{"tokens":{"access_token":"fake-token","account_id":"account-b"}}`), 0600); err != nil {
			t.Fatal(err)
		}
		f.refresh()
		if f.posts.Load() != 0 || f.state().Status != "failed" {
			t.Fatalf("state=%+v", f.state())
		}
	})
}

func TestResetOnceNoEligibleCreditsDisarms(t *testing.T) {
	for _, credits := range []string{`{"available_count":0,"credits":[]}`, `{"available_count":1,"credits":[{"id":"bad","status":"available","expires_at":"invalid"}]}`, `{"available_count":1,"credits":[{"id":"expired","status":"available","expires_at":"2000-01-01T00:00:00Z"}]}`, `{"available_count":1,"credits":[{"id":"spent","status":"redeemed","expires_at":"2099-01-01T00:00:00Z"}]}`} {
		t.Run(credits, func(t *testing.T) {
			f := newResetFixture(t)
			f.arm()
			f.credits.Store(credits)
			f.refresh()
			if f.posts.Load() != 0 || f.state().Status != "failed" {
				t.Fatalf("state=%+v", f.state())
			}
			f.restart(true)
			f.refresh()
			if f.lists.Load() != 1 {
				t.Fatal("waited for future credit")
			}
		})
	}
}

func TestResetOnceAmbiguousOutcomesNeverRetry(t *testing.T) {
	for _, code := range []int{200, 400, 429, 500} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			f := newResetFixture(t)
			f.arm()
			f.consumeCode.Store(int32(code))
			f.consumeBody.Store(`{}`)
			f.refresh()
			if f.posts.Load() != 1 || f.state().Status != "unknown" {
				t.Fatalf("posts=%d state=%+v", f.posts.Load(), f.state())
			}
			if _, err := f.app.ArmChatGPTResetOnce(f.now); err == nil {
				t.Fatal("rearmed unknown outcome")
			}
			if _, err := f.app.CancelChatGPTResetOnce(false, f.now); err == nil {
				t.Fatal("cleared unknown without acknowledgement")
			}
			f.restart(true)
			f.refresh()
			if f.posts.Load() != 1 {
				t.Fatal("retried after restart")
			}
			if _, err := f.app.CancelChatGPTResetOnce(true, f.now); err != nil {
				t.Fatal(err)
			}
			f.refresh()
			if f.posts.Load() != 1 {
				t.Fatal("acknowledgement spent credit")
			}
		})
	}
	t.Run("dropped-connection", func(t *testing.T) {
		f := newResetFixture(t)
		f.arm()
		f.dropResponse.Store(true)
		f.refresh()
		if f.posts.Load() != 1 || f.state().Status != "unknown" {
			t.Fatalf("posts=%d state=%+v", f.posts.Load(), f.state())
		}
		f.dropResponse.Store(false)
		f.restart(true)
		f.refresh()
		if f.posts.Load() != 1 {
			t.Fatal("retried after dropped consume response")
		}
	})
}

func TestResetOnceReconcilesUnknownAfterVerifiedSpend(t *testing.T) {
	f := newResetFixture(t)
	f.arm()
	f.consumeBody.Store(`{}`)
	f.refresh()
	if f.posts.Load() != 1 || f.state().Status != "unknown" || f.state().CreditID != "early" {
		t.Fatalf("posts=%d state=%+v", f.posts.Load(), f.state())
	}
	f.usage.Store(weeklyUsage(0))
	f.credits.Store(`{"available_count":1,"credits":[{"id":"late","status":"available","expires_at":"2099-02-01T00:00:00Z"}]}`)
	f.refresh()
	if f.posts.Load() != 1 {
		t.Fatal("reconcile posted another consume")
	}
	if f.state().Status != "consumed" {
		t.Fatalf("verified spend left unknown: %+v", f.state())
	}
}

func TestResetOnceDoesNotReconcileAmbiguousUnknown(t *testing.T) {
	t.Run("credit-still-available", func(t *testing.T) {
		f := newResetFixture(t)
		f.arm()
		f.consumeBody.Store(`{}`)
		f.refresh()
		f.usage.Store(weeklyUsage(0))
		f.refresh()
		if f.posts.Load() != 1 || f.state().Status != "unknown" {
			t.Fatalf("reconciled without missing credit: %+v", f.state())
		}
	})
	t.Run("weekly-still-exhausted", func(t *testing.T) {
		f := newResetFixture(t)
		f.arm()
		f.consumeBody.Store(`{}`)
		f.refresh()
		f.credits.Store(`{"available_count":1,"credits":[{"id":"late","status":"available","expires_at":"2099-02-01T00:00:00Z"}]}`)
		f.refresh()
		if f.posts.Load() != 1 || f.state().Status != "unknown" {
			t.Fatalf("reconciled while weekly still exhausted: %+v", f.state())
		}
	})
}

func TestResetOnceManualConsumeUsesSameSafetyBoundary(t *testing.T) {
	t.Run("manual-disarms", func(t *testing.T) {
		f := newResetFixture(t)
		f.arm()
		f.app.Cfg.Providers.ChatGPT.AllowResetConsume = true
		if _, err := f.app.ConsumeChatGPTResetCredit(context.Background(), "late", "manual-request", f.now); err != nil {
			t.Fatal(err)
		}
		f.refresh()
		if f.posts.Load() != 1 || f.state().CreditID != "late" || f.state().Status != "consumed" {
			t.Fatalf("state=%+v posts=%d", f.state(), f.posts.Load())
		}
	})
	t.Run("unknown-blocks-manual", func(t *testing.T) {
		f := newResetFixture(t)
		f.arm()
		f.consumeBody.Store(`{}`)
		f.refresh()
		f.app.Cfg.Providers.ChatGPT.AllowResetConsume = true
		if _, err := f.app.ConsumeChatGPTResetCredit(context.Background(), "late", "manual-request", f.now); err == nil {
			t.Fatal("manual consume bypassed unknown outcome")
		}
		if f.posts.Load() != 1 {
			t.Fatal("manual consume spent second credit")
		}
	})
	t.Run("legacy-gate-preserved", func(t *testing.T) {
		f := newResetFixture(t)
		f.arm()
		if _, err := f.app.ConsumeChatGPTResetCredit(context.Background(), "late", "manual-request", f.now); err == nil {
			t.Fatal("manual gate bypassed")
		}
		if f.posts.Load() != 0 || f.state().Status != "armed" {
			t.Fatal("disabled manual call changed state")
		}
	})
	t.Run("in-flight-blocks-controls", func(t *testing.T) {
		f := newResetFixture(t)
		f.arm()
		f.app.Cfg.Providers.ChatGPT.AllowResetConsume = true
		entered, release := make(chan struct{}), make(chan struct{})
		f.consumeHook = func() { close(entered); <-release }
		done := make(chan struct{})
		go func() { defer close(done); f.refresh() }()
		<-entered
		_, armErr := f.app.ArmChatGPTResetOnce(f.now)
		_, cancelErr := f.app.CancelChatGPTResetOnce(true, f.now)
		_, manualErr := f.app.ConsumeChatGPTResetCredit(context.Background(), "late", "manual-request", f.now)
		close(release)
		<-done
		if armErr == nil || cancelErr == nil || manualErr == nil {
			t.Fatalf("controls accepted during consume: arm=%v cancel=%v manual=%v", armErr, cancelErr, manualErr)
		}
		if f.posts.Load() != 1 {
			t.Fatal("concurrent manual consume spent extra credit")
		}
	})
}

func TestResetOnceConcurrentDaemonsSpendAtMostOnce(t *testing.T) {
	f := newResetFixture(t)
	f.arm()
	apps := []*App{f.app}
	for i := 0; i < 7; i++ {
		a := New(f.app.Cfg, f.app.Logger)
		a.EnableResetOnce()
		apps = append(apps, a)
	}
	var wg sync.WaitGroup
	for _, a := range apps {
		wg.Add(1)
		go func(a *App) { defer wg.Done(); a.RefreshOne(context.Background(), a.chatGPTProvider(), f.now) }(a)
	}
	wg.Wait()
	if f.posts.Load() != 1 {
		t.Fatalf("posts=%d", f.posts.Load())
	}
}

func TestResetOnceInvalidOrUnwritableStateFailsClosed(t *testing.T) {
	t.Run("corrupt", func(t *testing.T) {
		f := newResetFixture(t)
		f.arm()
		if err := os.WriteFile(f.app.resetOncePath(), []byte(`{"status":"armed"}`), 0600); err != nil {
			t.Fatal(err)
		}
		f.refresh()
		if _, err := f.app.ArmChatGPTResetOnce(f.now); err == nil {
			t.Fatal("overwrote corrupt state")
		}
		if f.posts.Load() != 0 || f.state().Status != "blocked" {
			t.Fatalf("state=%+v", f.state())
		}
	})
	t.Run("missing-path", func(t *testing.T) {
		f := newResetFixture(t)
		f.app.Cfg.Server.StatePath = ""
		if _, err := f.app.ArmChatGPTResetOnce(f.now); err == nil {
			t.Fatal("armed without durable storage")
		}
		f.refresh()
		if f.posts.Load() != 0 {
			t.Fatal("spent without storage")
		}
	})
	t.Run("claim-save-failure", func(t *testing.T) {
		f := newResetFixture(t)
		f.arm()
		f.listHook = func() {
			if err := os.Remove(f.app.resetOncePath()); err != nil {
				t.Error(err)
			}
			if err := os.Mkdir(f.app.resetOncePath(), 0700); err != nil {
				t.Error(err)
			}
		}
		f.refresh()
		if f.posts.Load() != 0 {
			t.Fatal("spent before durable claim")
		}
	})
}
