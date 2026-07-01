package imagesvc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/anomalyco/bootseed/server/internal/model"
)

func TestUpdateImageMetadata(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "index.json")
	initial := model.Index{
		SchemaVersion: 1,
		Images: []model.Image{{
			ID: "rocky9", Path: "/images/rocky9.raw.zst", Format: "raw.zst",
			CompressedSize: 1, RawSize: 2, SHA256Compressed: "abc",
		}},
	}
	data, err := json.Marshal(initial)
	if err != nil {
		t.Fatalf("marshal initial: %v", err)
	}
	if err := os.WriteFile(indexPath, data, 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}

	svc := New(indexPath, dir)
	err = svc.Update("rocky9", UpdateSpec{
		Name: "Rocky Linux 9", OS: "rocky", Version: "9.4",
		Architecture: "x86_64", Firmware: []string{"bios", "uefi"},
		Description: "通用测试镜像",
	})
	if err != nil {
		t.Fatalf("update image: %v", err)
	}

	idx, err := svc.List()
	if err != nil {
		t.Fatalf("list index: %v", err)
	}
	got := idx.Images[0]
	if got.Name != "Rocky Linux 9" || got.Description != "通用测试镜像" {
		t.Fatalf("metadata not updated: %+v", got)
	}
	if got.Path != "/images/rocky9.raw.zst" || got.SHA256Compressed != "abc" {
		t.Fatalf("immutable fields changed: %+v", got)
	}
}
