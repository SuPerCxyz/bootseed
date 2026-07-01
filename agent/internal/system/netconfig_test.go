package system

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadImportedNetConfigAndNetStatus(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	oldImported := importedConfigPath
	oldStatus := networkStatusPath
	importedConfigPath = filepath.Join(root, "imported.json")
	networkStatusPath = filepath.Join(root, "status.json")
	t.Cleanup(func() {
		importedConfigPath = oldImported
		networkStatusPath = oldStatus
	})

	if got := ReadImportedNetConfig(); got.Interface != "" {
		t.Fatalf("expected empty imported config, got %+v", got)
	}
	if got := ReadNetStatus(); got.Mode != "" {
		t.Fatalf("expected empty net status, got %+v", got)
	}

	if err := os.WriteFile(importedConfigPath, []byte(`{
		"iface":"eth2",
		"mac":"52:54:00:93:70:39",
		"address":"172.16.50.120",
		"prefix_len":24,
		"gateway":"172.16.50.1",
		"dns":["223.5.5.5","8.8.8.8"],
		"server_url":"http://192.168.100.161:8088"
	}`), 0o644); err != nil {
		t.Fatalf("write imported config: %v", err)
	}
	if err := os.WriteFile(networkStatusPath, []byte(`{
		"mode":"static",
		"status":"ok",
		"message":"restored eth2"
	}`), 0o644); err != nil {
		t.Fatalf("write status: %v", err)
	}

	cfg := ReadImportedNetConfig()
	if cfg.Interface != "eth2" || cfg.Address != "172.16.50.120" || cfg.ServerURL != "http://192.168.100.161:8088" {
		t.Fatalf("unexpected imported config: %+v", cfg)
	}
	if len(cfg.DNS) != 2 || cfg.DNS[0] != "223.5.5.5" {
		t.Fatalf("unexpected dns list: %+v", cfg.DNS)
	}

	st := ReadNetStatus()
	if st.Mode != "static" || st.Status != "ok" || st.Message != "restored eth2" {
		t.Fatalf("unexpected net status: %+v", st)
	}
}
