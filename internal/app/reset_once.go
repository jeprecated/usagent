package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jeprecated/usagent/internal/model"
	"github.com/jeprecated/usagent/internal/providers/chatgpt"
)

var ErrResetOnceBusy = errors.New("reset-once operation in progress; try status before retrying")

// EnableResetOnce is called only by the daemon, never by local CLI refreshes.
func (a *App) EnableResetOnce() { a.resetOnceDaemon.Store(true) }

func (a *App) resetOncePath() string {
	if a.Cfg.Server.StatePath == "" {
		return ""
	}
	return a.Cfg.Server.StatePath + ".reset-once.json"
}

// A separate file/lock is intentional: cached usage can be overwritten or
// refreshed locally, but must never resurrect a spent authorization. Every
// operation reloads under the same cross-process lock, including manual spends.
func (a *App) lockResetOnce() (func(), error) {
	if !a.resetMu.TryLock() {
		return nil, ErrResetOnceBusy
	}
	path := a.resetOncePath()
	if path == "" {
		return a.resetMu.Unlock, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		a.resetMu.Unlock()
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		a.resetMu.Unlock()
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		a.resetMu.Unlock()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrResetOnceBusy
		}
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close(); a.resetMu.Unlock() }, nil
}

func (a *App) readResetOnce() (model.ChatGPTResetOnce, error) {
	off := model.ChatGPTResetOnce{Version: 1, Status: "off"}
	path := a.resetOncePath()
	if path == "" {
		return off, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return off, nil
	}
	if err != nil {
		return off, err
	}
	var s model.ChatGPTResetOnce
	if err := json.Unmarshal(b, &s); err != nil {
		return off, err
	}
	if s.Version != 1 {
		return off, errors.New("invalid reset-once state version")
	}
	switch s.Status {
	case "armed":
		if s.AccountID == "" || s.ArmedAt <= 0 || s.CreditID != "" || s.RedeemRequestID != "" {
			return off, errors.New("invalid armed reset-once state")
		}
	case "unknown", "consumed":
		if s.AccountID == "" || s.CreditID == "" || s.RedeemRequestID == "" {
			return off, errors.New("invalid reset-once redemption state")
		}
	case "off", "cancelled", "failed":
	default:
		return off, errors.New("invalid reset-once status")
	}
	return s, nil
}

func (a *App) saveResetOnce(s model.ChatGPTResetOnce) error {
	path := a.resetOncePath()
	if path == "" {
		return errors.New("reset-once requires a durable server.statePath")
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".reset-once-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err = tmp.Write(b); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	// File fsync alone doesn't make a rename durable across a power failure.
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (a *App) ChatGPTResetOnceStatus() model.ChatGPTResetOnce {
	s, err := a.readResetOnce() // Atomic rename permits non-blocking, read-only status.
	if err != nil {
		return model.ChatGPTResetOnce{Version: 1, Status: "blocked", Message: "Cannot read valid reset-once state; no automatic redemption: " + err.Error()}
	}
	return s
}

func (a *App) ArmChatGPTResetOnce(now time.Time) (model.ChatGPTResetOnce, error) {
	if !a.resetOnceDaemon.Load() {
		return model.ChatGPTResetOnce{}, errors.New("reset-once requires the running daemon")
	}
	unlock, err := a.lockResetOnce()
	if err != nil {
		return model.ChatGPTResetOnce{}, err
	}
	defer unlock()
	s, err := a.readResetOnce()
	if err != nil {
		return s, err
	}
	if s.Status == "unknown" {
		return s, errors.New("redemption outcome unknown; reconcile credit usage before acknowledging cancellation")
	}
	p := a.chatGPTProvider()
	if p == nil {
		return s, errors.New("chatgpt provider is not enabled")
	}
	expected := ""
	if s.Status == "armed" {
		expected = s.AccountID
	}
	_, account, err := p.BindAccount(expected)
	if err != nil {
		return s, err
	}
	if s.Status == "armed" {
		return s, nil
	}
	s = model.ChatGPTResetOnce{Version: 1, Status: "armed", AccountID: account, ArmedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli(), Message: "Use one reset when the account-wide weekly quota reaches zero; earliest valid expiry first."}
	return s, a.saveResetOnce(s)
}

func (a *App) CancelChatGPTResetOnce(acknowledgeUnknown bool, now time.Time) (model.ChatGPTResetOnce, error) {
	unlock, err := a.lockResetOnce()
	if err != nil {
		return model.ChatGPTResetOnce{}, err
	}
	defer unlock()
	s, err := a.readResetOnce()
	if err != nil {
		return s, err
	}
	if s.Status == "unknown" && !acknowledgeUnknown {
		return s, errors.New("redemption outcome unknown; verify the account, then cancel with --acknowledge-unknown")
	}
	s.Status = "cancelled"
	s.UpdatedAt = now.UnixMilli()
	s.Message = "Reset-once cancelled; no automatic redemption."
	return s, a.saveResetOnce(s)
}

func (a *App) resetOnceFailed(s model.ChatGPTResetOnce, message string, now time.Time) {
	s.Status = "failed"
	s.Message = message
	s.UpdatedAt = now.UnixMilli()
	if err := a.saveResetOnce(s); err != nil {
		a.Logger.Error("reset-once disarm could not be persisted; no credit sent", "error", err)
	}
}

func (a *App) refreshChatGPTResetOnce(ctx context.Context, p *chatgpt.Provider, now time.Time) {
	// Hold the lock across the fresh check, credit selection, durable claim and
	// POST. Competing daemons/manual consumers/cancellation cannot interleave.
	unlock, err := a.lockResetOnce()
	if err != nil {
		a.Logger.Warn("reset-once refresh skipped", "error", err)
		return
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	s, err := a.readResetOnce()
	if err != nil {
		a.Logger.Error("reset-once blocked by invalid state", "error", err)
		result, fetchErr := p.Fetch(ctx, now)
		a.recordRefresh(p, result, fetchErr, now)
		return
	}
	if s.Status == "unknown" {
		a.reconcileUnknownResetOnce(ctx, p, s, now)
		return
	}
	if s.Status != "armed" {
		result, fetchErr := p.Fetch(ctx, now)
		a.recordRefresh(p, result, fetchErr, now)
		return
	}
	bound, _, err := p.BindAccount(s.AccountID)
	if err != nil {
		a.resetOnceFailed(s, err.Error(), now)
		return
	}
	result, exhausted, err := bound.FetchForReset(ctx, now)
	a.recordRefresh(p, result, err, now)
	if err != nil || !exhausted {
		return
	}
	credits, err := bound.ListResetCredits(ctx, time.Now())
	if err != nil {
		a.Logger.Warn("reset-once credit listing failed; no redemption", "error", err)
		return
	}
	credit, ok := earliestResetCredit(credits, time.Now())
	if !ok {
		a.resetOnceFailed(s, "No eligible unexpired credit; reset-once disarmed.", now)
		return
	}
	// Recheck after listing to avoid spending on stale zero usage if a natural
	// reset or an external redemption happened while the list was in flight.
	result, exhausted, err = bound.FetchForReset(ctx, time.Now())
	a.recordRefresh(p, result, err, time.Now())
	if err != nil || !exhausted {
		return
	}
	// Credentials may have changed during network calls. Never switch account;
	// also disarm rather than spending against a no-longer-selected account.
	if _, _, err = p.BindAccount(s.AccountID); err != nil {
		a.resetOnceFailed(s, err.Error(), now)
		return
	}
	expiry, _ := time.Parse(time.RFC3339Nano, credit.ExpiresAt)
	if !expiry.After(time.Now()) {
		a.resetOnceFailed(s, "Selected reset credit expired before redemption; disarmed.", now)
		return
	}
	if ctx.Err() != nil {
		return
	}
	if _, err := a.redeemResetCredit(ctx, bound, s, credit.ID, "", time.Now()); err != nil {
		a.Logger.Error("reset-once redemption stopped; will not retry an unknown outcome", "error", err)
		return
	}
	result, err = bound.Fetch(ctx, time.Now())
	a.recordRefresh(p, result, err, time.Now())
}

func (a *App) reconcileUnknownResetOnce(ctx context.Context, p *chatgpt.Provider, s model.ChatGPTResetOnce, now time.Time) {
	bound, _, err := p.BindAccount(s.AccountID)
	if err != nil {
		result, fetchErr := p.Fetch(ctx, now)
		a.recordRefresh(p, result, fetchErr, now)
		return
	}
	result, exhausted, err := bound.FetchForReset(ctx, now)
	a.recordRefresh(p, result, err, now)
	if err != nil {
		return
	}
	credits, err := bound.ListResetCredits(ctx, time.Now())
	if err != nil {
		a.Logger.Warn("reset-once reconcile could not list credits; leaving unknown", "error", err)
		return
	}
	if claimedCreditStillAvailable(credits, s.CreditID) || exhausted {
		return
	}
	s.Status = "consumed"
	s.UpdatedAt = time.Now().UnixMilli()
	s.Message = "One reset consumed; confirmed by missing claimed credit and weekly recovery. Reset-once is disarmed."
	if err := a.saveResetOnce(s); err != nil {
		a.Logger.Error("reset-once reconcile not durable; leaving unknown", "error", err)
		return
	}
}

func claimedCreditStillAvailable(credits model.ChatGPTResetCreditsResponse, creditID string) bool {
	for _, c := range credits.Credits {
		if c.ID == creditID && c.Status == "available" {
			return true
		}
	}
	return false
}

func earliestResetCredit(credits model.ChatGPTResetCreditsResponse, now time.Time) (model.ChatGPTResetCredit, bool) {
	var selected model.ChatGPTResetCredit
	var earliest time.Time
	if credits.AvailableCount <= 0 {
		return selected, false
	}
	for _, c := range credits.Credits {
		expires, err := time.Parse(time.RFC3339Nano, c.ExpiresAt)
		if c.Status != "available" || strings.TrimSpace(c.ID) == "" || err != nil || !expires.After(now) {
			continue
		}
		if selected.ID == "" || expires.Before(earliest) || (expires.Equal(earliest) && c.ID < selected.ID) {
			selected = c
			earliest = expires
		}
	}
	return selected, selected.ID != ""
}

// Both manual and automatic redemption hold lockResetOnce and pass through
// this durable claim. No retries are made, even for apparently transient errors.
func (a *App) redeemResetCredit(ctx context.Context, p *chatgpt.Provider, s model.ChatGPTResetOnce, creditID, requestID string, now time.Time) (model.ChatGPTResetConsumeResponse, error) {
	if requestID == "" {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return model.ChatGPTResetConsumeResponse{}, err
		}
		requestID = hex.EncodeToString(id[:])
	}
	s.Status = "unknown"
	s.CreditID, s.RedeemRequestID = creditID, requestID
	s.UpdatedAt = now.UnixMilli()
	s.Message = "Redemption started; outcome not yet confirmed. Never auto-retry. Verify the account before acknowledging cancellation."
	if err := a.saveResetOnce(s); err != nil {
		return model.ChatGPTResetConsumeResponse{}, fmt.Errorf("claim not durable; no credit sent: %w", err)
	}
	res, err := p.ConsumeResetCredit(ctx, creditID, requestID, now)
	if err != nil {
		return res, fmt.Errorf("redemption outcome unknown; do not retry: %w", err)
	}
	s.Status, s.Message = "consumed", "One reset consumed; reset-once is disarmed."
	s.UpdatedAt = time.Now().UnixMilli()
	if err := a.saveResetOnce(s); err != nil {
		return res, fmt.Errorf("credit consumed but completion not durable; do not retry: %w", err)
	}
	return res, nil
}

func (a *App) consumeManualResetCredit(ctx context.Context, p *chatgpt.Provider, creditID, requestID string, now time.Time) (model.ChatGPTResetConsumeResponse, error) {
	if strings.TrimSpace(creditID) == "" {
		return model.ChatGPTResetConsumeResponse{}, errors.New("creditId is required")
	}
	unlock, err := a.lockResetOnce()
	if err != nil {
		return model.ChatGPTResetConsumeResponse{}, err
	}
	defer unlock()
	s, err := a.readResetOnce()
	if err != nil {
		return model.ChatGPTResetConsumeResponse{}, err
	}
	if s.Status == "unknown" {
		return model.ChatGPTResetConsumeResponse{}, errors.New("redemption outcome unknown; reconcile before another manual consume")
	}
	expected := ""
	if s.Status == "armed" {
		expected = s.AccountID
	}
	bound, account, err := p.BindAccount(expected)
	if err != nil {
		return model.ChatGPTResetConsumeResponse{}, err
	}
	s.AccountID = account
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := a.redeemResetCredit(ctx, bound, s, strings.TrimSpace(creditID), requestID, now)
	if err != nil {
		return res, err
	}
	result, fetchErr := bound.Fetch(ctx, time.Now())
	a.recordRefresh(p, result, fetchErr, time.Now())
	return res, nil
}
