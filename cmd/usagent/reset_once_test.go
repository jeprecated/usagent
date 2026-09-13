package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootResetOnceDispatchAndHelp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/chatgpt/reset-once/arm" {
			t.Errorf("request=%s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"version":1,"status":"armed"}`)
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("client: {mode: prefer-daemon}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := runWithIO([]string{"reset-once", "arm", "--config", path, "--daemon-url", srv.URL}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "ChatGPT reset-once: armed") {
		t.Fatalf("stdout=%s", stdout.String())
	}
	for _, args := range [][]string{{"--help"}, {"reset-once", "--help"}, {"reset-once", "arm", "--help"}} {
		stdout.Reset()
		if err := runWithIO(args, &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout.String(), "reset-once") {
			t.Fatalf("missing help: %s", stdout.String())
		}
	}
}
