package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jeprecated/usagent/internal/model"
)

func resetCLIConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server: {host: '127.0.0.1', port: 1}\nclient: {mode: prefer-daemon}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResetOnceCLICommands(t *testing.T) {
	configPath := resetCLIConfig(t)
	for _, action := range []string{"arm", "status", "cancel"} {
		t.Run(action, func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				path := "/v1/chatgpt/reset-once"
				method := "GET"
				if action != "status" {
					path += "/" + action
					method = "POST"
				}
				if r.URL.Path != path || r.Method != method {
					t.Errorf("got %s %s", r.Method, r.URL.Path)
				}
				if action != "status" {
					var body struct {
						Confirm            string `json:"confirm"`
						AcknowledgeUnknown bool   `json:"acknowledgeUnknown"`
					}
					if json.NewDecoder(r.Body).Decode(&body) != nil || body.Confirm != action+"-chatgpt-reset-once" || r.Header.Get("X-Usagent-Action") != body.Confirm || r.Header.Get("Content-Type") != "application/json" {
						t.Error("missing explicit confirmation")
					}
					if body.AcknowledgeUnknown != (action == "cancel") {
						t.Error("wrong acknowledgement")
					}
				}
				fmt.Fprint(w, `{"version":1,"status":"armed","message":"weekly zero; one credit"}`)
			}))
			defer srv.Close()
			args := []string{action, "--config", configPath, "--daemon-url", srv.URL, "--json"}
			if action == "cancel" {
				args = append(args, "--acknowledge-unknown")
			}
			var stdout, stderr bytes.Buffer
			if err := RunResetOnce(context.Background(), args, &stdout, &stderr); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(stdout.String(), `"status": "armed"`) || hits.Load() != 1 {
				t.Fatalf("output=%s hits=%d", stdout.String(), hits.Load())
			}
		})
	}
}

func TestResetOnceCLINeverFallsBackOrFollowsRedirects(t *testing.T) {
	configPath := resetCLIConfig(t)
	for _, code := range []int{307, 308, 500, 403} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Header().Set("Location", "/other")
				w.WriteHeader(code)
				fmt.Fprint(w, `{"error":{"message":"state uncertain; do not retry"}}`)
			}))
			defer srv.Close()
			var stdout, stderr bytes.Buffer
			err := RunResetOnce(context.Background(), []string{"arm", "--config", configPath, "--daemon-url", srv.URL}, &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), "do not retry") || hits.Load() != 1 || stdout.Len() != 0 {
				t.Fatalf("err=%v hits=%d output=%q", err, hits.Load(), stdout.String())
			}
			if strings.Contains(stderr.String(), "refreshing locally") {
				t.Fatal("attempted fallback")
			}
		})
	}
}

func TestResetOnceCLIRejectsInvalidAndRemoteCommands(t *testing.T) {
	configPath := resetCLIConfig(t)
	for _, args := range [][]string{{}, {"toggle"}, {"arm", "--offline"}, {"arm", "--acknowledge-unknown"}, {"arm", "--daemon-url", "http://remote.example:8787"}, {"arm", "--daemon-url", "http://192.0.2.1:8787"}} {
		var stdout, stderr bytes.Buffer
		if err := RunResetOnce(context.Background(), append(args, "--config", configPath), &stdout, &stderr); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestResetOnceStatusAppearsInUsage(t *testing.T) {
	for _, status := range []string{"armed", "unknown", "consumed", "failed", "blocked", "cancelled"} {
		usage := model.Usage{Providers: []model.Provider{{ID: "chatgpt", Label: "ChatGPT"}}, ChatGPTResetOnce: &model.ChatGPTResetOnce{Version: 1, Status: status, Message: "test message"}}
		output := FormatUsage(usage)
		if !strings.Contains(output, "ChatGPT reset-once: "+status) || !strings.Contains(output, "test message") {
			t.Fatalf("missing status: %s", output)
		}
	}
}
