package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jeprecated/usagent/internal/app"
	"github.com/jeprecated/usagent/internal/config"
	"github.com/jeprecated/usagent/internal/model"
	"github.com/jeprecated/usagent/internal/resetevents"
)

func TestResetEventFeed(t *testing.T) {
	cfg := config.Default()
	cfg.Server.StatePath = ""
	a := app.New(cfg, nil)
	if err := a.EnableResetEvents(); err != nil {
		t.Fatal(err)
	}
	q := model.QuotaItem{ID: "weekly", Visible: true, Limit: 100, Remaining: 4}
	if err := a.ResetEvents.Record("test", []model.QuotaItem{q}, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	q.Remaining = 100
	if err := a.ResetEvents.Record("test", []model.QuotaItem{q}, time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	handler := New(a, "")
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		return r
	}
	res := request("/v1/reset-events")
	if res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	var page resetevents.Page
	if err := json.Unmarshal(res.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.StreamID == "" {
		t.Fatalf("invalid feed %+v", page)
	}
	res = request("/v1/reset-events?stream=" + page.StreamID + "&after=1")
	if err := json.Unmarshal(res.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 0 {
		t.Fatal("cursor ignored")
	}
	for _, path := range []string{"/v1/reset-events?after=-1", "/v1/reset-events?after=not-a-number"} {
		if res := request(path); res.Code != 400 {
			t.Fatalf("accepted invalid cursor %s", path)
		}
	}
}
