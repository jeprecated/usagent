package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadCLIProxy(t *testing.T, yaml string) (CLIProxyConfig, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	return cfg.Providers.CLIProxy, err
}

func TestLoadCLIProxyDefaults(t *testing.T) {
	c, err := loadCLIProxy(t, "providers:\n  cliproxy:\n    enabled: true\n    refreshMs: 1\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseURL != DefaultCLIProxyBaseURL || c.ManagementKeyEnv != "CLIPROXY_MANAGEMENT_KEY" || c.RefreshMs != MinCLIProxyRefreshMs || c.StaleMs != c.RefreshMs*3 {
		t.Fatalf("config=%+v", c)
	}
}

func TestCLIProxyBaseURLKeepsManagementKeyOffPlainRemoteHTTP(t *testing.T) {
	for _, base := range []string{"https://cliproxyapi.example.ts.net", "http://localhost:8317/", "http://[::1]:8317"} {
		if _, err := loadCLIProxy(t, "providers:\n  cliproxy:\n    enabled: true\n    baseUrl: \""+base+"\"\n"); err != nil {
			t.Errorf("%s: %v", base, err)
		}
	}
	for _, base := range []string{"http://lattice:8317", "https://host/v8/management", "https://user:pw@host"} {
		if _, err := loadCLIProxy(t, "providers:\n  cliproxy:\n    enabled: true\n    baseUrl: \""+base+"\"\n"); err == nil || !strings.Contains(err.Error(), "baseUrl") {
			t.Errorf("%s: err = %v", base, err)
		}
	}
}

func TestCLIProxyManagementKeyFileExpandsRuntimeDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	c, err := loadCLIProxy(t, "providers:\n  cliproxy:\n    managementKeyFile: \"${XDG_RUNTIME_DIR}/agenix/key\"\n")
	if err != nil || c.ManagementKeyFile != "/run/user/1000/agenix/key" {
		t.Fatalf("file=%q err=%v", c.ManagementKeyFile, err)
	}
}
