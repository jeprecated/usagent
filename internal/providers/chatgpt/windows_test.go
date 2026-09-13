package chatgpt

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestWindowLabelsFollowDurationNotPosition(t *testing.T) {
	var payload usagePayload
	err := json.Unmarshal([]byte(`{
   "rate_limit":{"primary_window":{"used_percent":97,"limit_window_seconds":604800}},
   "additional_rate_limits":[{"display_name":"Spark","primary_window":{"used_percent":0,"limit_window_seconds":604800},"secondary_window":{"used_percent":0,"limit_window_seconds":18000}}]
 }`), &payload)
	if err != nil {
		t.Fatal(err)
	}
	items := Normalize(payload, time.Now(), 1000, 2000)
	wantLabels := []string{"ChatGPT weekly", "Spark weekly", "Spark 5h"}
	wantWindows := []string{"weekly", "weekly", "session"}
	for i, item := range items {
		if item.Label != wantLabels[i] || item.Window.ID != wantWindows[i] {
			t.Errorf("item %d: label=%q window=%q, want %q/%q", i, item.Label, item.Window.ID, wantLabels[i], wantWindows[i])
		}
	}
	if len(items) != len(wantLabels) {
		t.Fatalf("items=%+v", items)
	}
}

func TestWindowPreservesFractionalRemaining(t *testing.T) {
	item := windowItem("chatgpt-primary", "ChatGPT", usageWindow{UsedPercent: 99.9, LimitWindowSeconds: 604800}, time.Now(), 1000, 2000)
	if math.Abs(item.Remaining-0.1) > 1e-9 || item.PercentUsed != 99.9 {
		t.Fatalf("fractional quota must not become exhausted: %+v", item)
	}
}

func TestWindowLabelsUnknownDurationHonestly(t *testing.T) {
	item := windowItem("chatgpt-primary", "ChatGPT", usageWindow{LimitWindowSeconds: 0}, time.Now(), 1000, 2000)
	if item.Label != "ChatGPT window" || item.Window.ID != "window" {
		t.Fatalf("item=%+v", item)
	}
}
