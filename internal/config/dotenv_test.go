package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyEnvFileKeepsExistingValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "CONFIG_ENV=prod\n# comment\nexport CONFIG_APP_ID=table-server\nCONFIG_TOKEN=\"cfg_example\"\nBROKEN\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_ENV", "dev")
	t.Setenv("CONFIG_APP_ID", "")
	t.Setenv("CONFIG_TOKEN", "")
	applyEnvFile(path)
	if got := os.Getenv("CONFIG_ENV"); got != "dev" {
		t.Fatalf("CONFIG_ENV = %q", got)
	}
	if got := os.Getenv("CONFIG_APP_ID"); got != "table-server" {
		t.Fatalf("CONFIG_APP_ID = %q", got)
	}
	if got := os.Getenv("CONFIG_TOKEN"); got != "cfg_example" {
		t.Fatalf("CONFIG_TOKEN = %q", got)
	}
}
