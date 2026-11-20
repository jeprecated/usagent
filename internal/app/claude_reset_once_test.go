package app

import (
	"testing"
	"time"

	"github.com/jeprecated/usagent/internal/model"
	"github.com/jeprecated/usagent/internal/providers/claude"
)

func TestEarliestClaudeResetCreditSkipsFableResets(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	credits := model.ClaudeResetCreditsResponse{AvailableCount: 2, Credits: []model.ClaudeResetCredit{
		{ID: "fable", Kind: claude.ResetKindFable, Status: "available", ExpiresAt: "2026-11-25T00:00:00Z"},
		{ID: "weekly", Kind: claude.ResetKindWeekly, Status: "available", ExpiresAt: "2026-12-10T22:00:00Z"},
	}}
	if got, ok := earliestClaudeResetCredit(credits, now); !ok || got.ID != "weekly" {
		t.Fatalf("selected=%+v ok=%v, want the weekly reset", got, ok)
	}
	credits.Credits = credits.Credits[:1]
	if got, ok := earliestClaudeResetCredit(credits, now); ok {
		t.Fatalf("selected Fable reset %+v for weekly exhaustion", got)
	}
}
