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
	"github.com/jeprecated/usagent/internal/providers/claude"
)

func (a *App) claudeResetOncePath() string {
	if a.Cfg.Server.StatePath == "" {
		return ""
	}
	return a.Cfg.Server.StatePath + ".claude-reset-once.json"
}

func (a *App) lockClaudeResetOnce() (func(), error) {
	if !a.claudeResetMu.TryLock() {
		return nil, ErrResetOnceBusy
	}
	path := a.claudeResetOncePath()
	if path == "" {
		return a.claudeResetMu.Unlock, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		a.claudeResetMu.Unlock()
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		a.claudeResetMu.Unlock()
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		a.claudeResetMu.Unlock()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrResetOnceBusy
		}
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close(); a.claudeResetMu.Unlock() }, nil
}

func (a *App) readClaudeResetOnce() (model.ClaudeResetOnce, error) {
	off := model.ClaudeResetOnce{Version: 1, Status: "off"}
	path := a.claudeResetOncePath()
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
	var s model.ClaudeResetOnce
	if err := json.Unmarshal(b, &s); err != nil {
		return off, err
	}
	if s.Version != 1 {
		return off, errors.New("invalid claude reset-once state version")
	}
	switch s.Status {
	case "armed":
		if s.AccountID == "" || s.ArmedAt <= 0 || s.CreditID != "" || s.RedeemRequestID != "" {
			return off, errors.New("invalid armed claude reset-once state")
		}
	case "unknown", "consumed":
		if s.AccountID == "" || s.CreditID == "" || s.RedeemRequestID == "" {
			return off, errors.New("invalid claude reset-once redemption state")
		}
	case "off", "cancelled", "failed":
	default:
		return off, errors.New("invalid claude reset-once status")
	}
	return s, nil
}

func (a *App) saveClaudeResetOnce(s model.ClaudeResetOnce) error {
	path := a.claudeResetOncePath()
	if path == "" {
		return errors.New("reset-once requires a durable server.statePath")
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".claude-reset-once-*")
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
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (a *App) ClaudeResetOnceStatus() model.ClaudeResetOnce {
	s, err := a.readClaudeResetOnce()
	if err != nil {
		return model.ClaudeResetOnce{Version: 1, Status: "blocked", Message: "Cannot read valid reset-once state; no automatic redemption: " + err.Error()}
	}
	return s
}

func (a *App) ArmClaudeResetOnce(now time.Time) (model.ClaudeResetOnce, error) {
	if !a.resetOnceDaemon.Load() {
		return model.ClaudeResetOnce{}, errors.New("reset-once requires the running daemon")
	}
	unlock, err := a.lockClaudeResetOnce()
	if err != nil {
		return model.ClaudeResetOnce{}, err
	}
	defer unlock()
	s, err := a.readClaudeResetOnce()
	if err != nil {
		return s, err
	}
	if s.Status == "unknown" {
		return s, errors.New("redemption outcome unknown; reconcile credit usage before acknowledging cancellation")
	}
	p := a.claudeProvider()
	if p == nil {
		return s, errors.New("claude-code provider is not enabled")
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
	s = model.ClaudeResetOnce{Version: 1, Status: "armed", AccountID: account, ArmedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli(), Message: "Use one reset when the account-wide weekly quota reaches zero; earliest valid expiry first."}
	return s, a.saveClaudeResetOnce(s)
}

func (a *App) CancelClaudeResetOnce(acknowledgeUnknown bool, now time.Time) (model.ClaudeResetOnce, error) {
	unlock, err := a.lockClaudeResetOnce()
	if err != nil {
		return model.ClaudeResetOnce{}, err
	}
	defer unlock()
	s, err := a.readClaudeResetOnce()
	if err != nil {
		return s, err
	}
	if s.Status == "unknown" && !acknowledgeUnknown {
		return s, errors.New("redemption outcome unknown; verify the account, then cancel with --acknowledge-unknown")
	}
	s.Status = "cancelled"
	s.UpdatedAt = now.UnixMilli()
	s.Message = "Reset-once cancelled; no automatic redemption."
	return s, a.saveClaudeResetOnce(s)
}

func (a *App) claudeResetOnceFailed(s model.ClaudeResetOnce, message string, now time.Time) {
	s.Status = "failed"
	s.Message = message
	s.UpdatedAt = now.UnixMilli()
	if err := a.saveClaudeResetOnce(s); err != nil {
		a.Logger.Error("claude reset-once disarm could not be persisted; no credit sent", "error", err)
	}
}

func (a *App) refreshClaudeResetOnce(ctx context.Context, p *claude.Provider, now time.Time) {
	unlock, err := a.lockClaudeResetOnce()
	if err != nil {
		a.Logger.Warn("claude reset-once refresh skipped", "error", err)
		return
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	s, err := a.readClaudeResetOnce()
	if err != nil {
		a.Logger.Error("claude reset-once blocked by invalid state", "error", err)
		result, fetchErr := p.Fetch(ctx, now)
		a.recordRefresh(p, result, fetchErr, now)
		return
	}
	if s.Status == "unknown" {
		a.reconcileUnknownClaudeResetOnce(ctx, p, s, now)
		return
	}
	if s.Status != "armed" {
		result, fetchErr := p.Fetch(ctx, now)
		a.recordRefresh(p, result, fetchErr, now)
		return
	}
	bound, _, err := p.BindAccount(s.AccountID)
	if err != nil {
		a.claudeResetOnceFailed(s, err.Error(), now)
		return
	}
	result, exhausted, err := bound.FetchForReset(ctx, now)
	a.recordRefresh(p, result, err, now)
	if err != nil || !exhausted {
		return
	}
	credits, err := bound.ListResetCredits(ctx, time.Now())
	if err != nil {
		a.Logger.Warn("claude reset-once credit listing failed; no redemption", "error", err)
		return
	}
	credit, ok := earliestClaudeResetCredit(credits, time.Now())
	if !ok {
		a.claudeResetOnceFailed(s, "No eligible unexpired credit; reset-once disarmed.", now)
		return
	}
	result, exhausted, err = bound.FetchForReset(ctx, time.Now())
	a.recordRefresh(p, result, err, time.Now())
	if err != nil || !exhausted {
		return
	}
	if _, _, err = p.BindAccount(s.AccountID); err != nil {
		a.claudeResetOnceFailed(s, err.Error(), now)
		return
	}
	expiry, _ := time.Parse(time.RFC3339Nano, credit.ExpiresAt)
	if !expiry.After(time.Now()) {
		if parsed, err := time.Parse(time.RFC3339, credit.ExpiresAt); err == nil {
			expiry = parsed
		}
	}
	if !expiry.After(time.Now()) {
		a.claudeResetOnceFailed(s, "Selected reset credit expired before redemption; disarmed.", now)
		return
	}
	if ctx.Err() != nil {
		return
	}
	if _, err := a.redeemClaudeResetCredit(ctx, bound, s, credit.ID, "", time.Now()); err != nil {
		a.Logger.Error("claude reset-once redemption stopped; will not retry an unknown outcome", "error", err)
		return
	}
	result, err = bound.Fetch(ctx, time.Now())
	a.recordRefresh(p, result, err, time.Now())
}

func (a *App) reconcileUnknownClaudeResetOnce(ctx context.Context, p *claude.Provider, s model.ClaudeResetOnce, now time.Time) {
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
		a.Logger.Warn("claude reset-once reconcile could not list credits; leaving unknown", "error", err)
		return
	}
	if claimedClaudeCreditStillAvailable(credits, s.CreditID) || exhausted {
		return
	}
	s.Status = "consumed"
	s.UpdatedAt = time.Now().UnixMilli()
	s.Message = "One reset consumed; confirmed by missing claimed credit and weekly recovery. Reset-once is disarmed."
	if err := a.saveClaudeResetOnce(s); err != nil {
		a.Logger.Error("claude reset-once reconcile not durable; leaving unknown", "error", err)
		return
	}
}

func claimedClaudeCreditStillAvailable(credits model.ClaudeResetCreditsResponse, creditID string) bool {
	for _, c := range credits.Credits {
		if c.ID == creditID && c.Status == "available" {
			return true
		}
	}
	return false
}

func earliestClaudeResetCredit(credits model.ClaudeResetCreditsResponse, now time.Time) (model.ClaudeResetCredit, bool) {
	var selected model.ClaudeResetCredit
	var earliest time.Time
	if credits.AvailableCount <= 0 {
		return selected, false
	}
	for _, c := range credits.Credits {
		expires, ok := parseCreditExpiry(c.ExpiresAt)
		if c.Status != "available" || strings.TrimSpace(c.ID) == "" || !ok || !expires.After(now) {
			continue
		}
		if selected.ID == "" || expires.Before(earliest) || (expires.Equal(earliest) && c.ID < selected.ID) {
			selected = c
			earliest = expires
		}
	}
	return selected, selected.ID != ""
}

func parseCreditExpiry(v string) (time.Time, bool) {
	if ts, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return ts, true
	}
	if ts, err := time.Parse(time.RFC3339, v); err == nil {
		return ts, true
	}
	return time.Time{}, false
}

func (a *App) redeemClaudeResetCredit(ctx context.Context, p *claude.Provider, s model.ClaudeResetOnce, creditID, requestID string, now time.Time) (model.ClaudeResetConsumeResponse, error) {
	if requestID == "" {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return model.ClaudeResetConsumeResponse{}, err
		}
		requestID = hex.EncodeToString(id[:])
	}
	s.Status = "unknown"
	s.CreditID, s.RedeemRequestID = creditID, requestID
	s.UpdatedAt = now.UnixMilli()
	s.Message = "Redemption started; outcome not yet confirmed. Never auto-retry. Verify the account before acknowledging cancellation."
	if err := a.saveClaudeResetOnce(s); err != nil {
		return model.ClaudeResetConsumeResponse{}, fmt.Errorf("claim not durable; no credit sent: %w", err)
	}
	res, err := p.ConsumeResetCredit(ctx, creditID, requestID, now)
	if err != nil {
		return res, fmt.Errorf("redemption outcome unknown; do not retry: %w", err)
	}
	s.Status, s.Message = "consumed", "One reset consumed; reset-once is disarmed."
	s.UpdatedAt = time.Now().UnixMilli()
	if err := a.saveClaudeResetOnce(s); err != nil {
		return res, fmt.Errorf("credit consumed but completion not durable; do not retry: %w", err)
	}
	return res, nil
}

func (a *App) consumeManualClaudeResetCredit(ctx context.Context, p *claude.Provider, creditID, requestID string, now time.Time) (model.ClaudeResetConsumeResponse, error) {
	if strings.TrimSpace(creditID) == "" {
		return model.ClaudeResetConsumeResponse{}, errors.New("creditId is required")
	}
	unlock, err := a.lockClaudeResetOnce()
	if err != nil {
		return model.ClaudeResetConsumeResponse{}, err
	}
	defer unlock()
	s, err := a.readClaudeResetOnce()
	if err != nil {
		return model.ClaudeResetConsumeResponse{}, err
	}
	if s.Status == "unknown" {
		return model.ClaudeResetConsumeResponse{}, errors.New("redemption outcome unknown; reconcile before another manual consume")
	}
	expected := ""
	if s.Status == "armed" {
		expected = s.AccountID
	}
	bound, account, err := p.BindAccount(expected)
	if err != nil {
		return model.ClaudeResetConsumeResponse{}, err
	}
	s.AccountID = account
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := a.redeemClaudeResetCredit(ctx, bound, s, strings.TrimSpace(creditID), requestID, now)
	if err != nil {
		return res, err
	}
	result, fetchErr := bound.Fetch(ctx, time.Now())
	a.recordRefresh(p, result, fetchErr, time.Now())
	return res, nil
}
