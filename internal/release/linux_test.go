package release

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxPublicationRequiresBothVersionedArchitectures(t *testing.T) {
	dir := t.TempDir()
	if _, err := platformAssets(dir, "1.2.3", "linux"); err == nil {
		t.Fatal("missing archive accepted")
	}
	path := filepath.Join(dir, "zwai-1.2.3-linux-arm64.tar.gz")
	if err := os.WriteFile(path, []byte("archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := platformAssets(dir, "1.2.3", "linux"); err == nil {
		t.Fatal("arm64-only publication omits x86 Linux users")
	}
	amd64 := filepath.Join(dir, "zwai-1.2.3-linux-amd64.tar.gz")
	if err := os.WriteFile(amd64, []byte("archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := platformAssets(dir, "v1.2.3", "linux")
	if err != nil || len(files) != 2 || files[0] != amd64 || files[1] != path {
		t.Fatalf("%v %v", files, err)
	}
}
