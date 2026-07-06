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

func TestRootUsageFlagsDispatchToUsageCommand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[{"id":"alpha","label":"Alpha","window":{"id":"week","label":"W","kind":"weekly"},"unit":"tokens","limit":100,"used":25,"remaining":75,"percentUsed":25}]}`)
	}))
	defer srv.Close()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	statePath := filepath.Join(dir, "snapshot.json")
	configText := fmt.Sprintf(`server:
  host: "127.0.0.1"
  port: 1
  statePath: %q
providers:
  custom:
    - id: mine
      label: Mine
      enabled: true
      endpoints:
        - id: quota
          url: %q
          itemsPath: items
          item:
            id: id
            label: label
            window: { id: window.id, label: window.label, kind: window.kind }
            unit: unit
            limit: limit
            used: used
            remaining: remaining
            percentUsed: percentUsed
            visible: true
usageView:
  providers: [mine]
quota:
  refreshMs: 300000
`, statePath, srv.URL)
	if err := os.WriteFile(configPath, []byte(configText), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := runWithIO([]string{"--config", configPath, "--offline", "--json"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"service": "usagent"`) || !strings.Contains(stdout.String(), `"provider": "mine"`) {
		t.Fatalf("stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}
