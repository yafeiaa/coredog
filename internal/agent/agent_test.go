package agent

import (
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/DomineCore/coredog/internal/coreparser"
)

func TestBuildCoredumpInfoWithCoreInfo(t *testing.T) {
	coreInfo := &coreparser.CoreInfo{
		ExecutablePath: "/usr/bin/python3",
		ProcessName:    "python3",
		FileSize:       1234,
		MD5:            "deadbeef",
	}
	info := buildCoredumpInfo("/corefile/ns/pod/app/core.python3.1.h.1", "http://example.com/core", coreInfo)

	if info.FileName != "core.python3.1.h.1" {
		t.Fatalf("unexpected FileName: %s", info.FileName)
	}
	if info.FilePath != "/corefile/ns/pod/app/core.python3.1.h.1" {
		t.Fatalf("unexpected FilePath: %s", info.FilePath)
	}
	if info.FileURL != "http://example.com/core" {
		t.Fatalf("unexpected FileURL: %s", info.FileURL)
	}
	if info.MD5 != "deadbeef" || info.FileSize != 1234 || info.ExecutablePath != "/usr/bin/python3" {
		t.Fatalf("coreInfo fields not passed through: %+v", info)
	}
}

func TestBuildCoredumpInfoNilCoreInfo(t *testing.T) {
	// 模拟非 ELF 的应用层 dump（如 sglang crash_dump_*.pkl）
	content := []byte("\x80\x04\x95fake-pickle-payload")
	dir := t.TempDir()
	path := filepath.Join(dir, "crash_dump_2026-01-01_00-00-00.pkl")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	info := buildCoredumpInfo(path, "http://example.com/dump", nil)

	if info.FileName != "crash_dump_2026-01-01_00-00-00.pkl" {
		t.Fatalf("unexpected FileName: %s", info.FileName)
	}
	if info.FileSize != int64(len(content)) {
		t.Fatalf("FileSize = %d, want %d", info.FileSize, len(content))
	}
	wantMD5 := fmt.Sprintf("%x", md5.Sum(content))
	if info.MD5 != wantMD5 {
		t.Fatalf("MD5 = %s, want %s", info.MD5, wantMD5)
	}
	if info.ExecutablePath != "" {
		t.Fatalf("ExecutablePath should be empty for non-ELF files, got %s", info.ExecutablePath)
	}
}

func TestBuildCoredumpInfoNilCoreInfoMissingFile(t *testing.T) {
	// 文件已被清理时不应 panic，仅返回零值大小/空 MD5
	info := buildCoredumpInfo(filepath.Join(t.TempDir(), "gone.pkl"), "url", nil)
	if info.FileSize != 0 || info.MD5 != "" {
		t.Fatalf("expected zero-value size/md5 for missing file, got %+v", info)
	}
}
