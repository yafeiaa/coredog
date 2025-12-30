package coreparser

import (
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

// md5Semaphore 用于限制 MD5 计算的并发度，防止大规模 coredump 时给系统造成性能影响
// 默认限制为 2 个并发，因为 MD5 计算是 IO 密集型操作
var md5Semaphore = make(chan struct{}, 2)

// CoreInfo 包含从 core 文件解析出的信息
type CoreInfo struct {
	ExecutablePath string // 可执行文件的完整路径
	ProcessName    string // 进程名称
	FileSize       int64  // core 文件大小
	MD5            string // core 文件 MD5
}

// SetMD5Concurrency 设置 MD5 计算的最大并发数
// 应在程序启动时调用，默认为 2
func SetMD5Concurrency(n int) {
	if n < 1 {
		n = 1
	}
	if n > 10 {
		n = 10 // 最大不超过 10
	}
	md5Semaphore = make(chan struct{}, n)
}

// ParseCoreFile 解析 core 文件获取详细信息
// 使用 file 命令获取可执行程序路径
func ParseCoreFile(corefilePath string) (*CoreInfo, error) {
	info := &CoreInfo{}

	// 1. 获取文件大小
	fi, err := os.Stat(corefilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat core file: %w", err)
	}
	info.FileSize = fi.Size()

	// 2. 计算 MD5（必须成功，带并发限制）
	md5Hash, err := calculateMD5(corefilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate MD5: %w", err)
	}
	info.MD5 = md5Hash

	// 3. 使用 file 命令获取可执行文件路径
	if err := parseWithFileCommand(corefilePath, info); err != nil {
		return nil, fmt.Errorf("failed to parse executable path: %w", err)
	}

	// 4. 从路径提取进程名
	info.ProcessName = GetProcessNameFromPath(info.ExecutablePath)

	return info, nil
}

// calculateMD5 计算文件的 MD5 哈希
// 使用信号量限制并发度，防止大量 coredump 同时计算 MD5 导致系统负载过高
func calculateMD5(filePath string) (string, error) {
	// 获取信号量，限制并发
	md5Semaphore <- struct{}{}
	defer func() { <-md5Semaphore }()

	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// parseWithFileCommand 使用 file 命令获取 core 文件信息
func parseWithFileCommand(corefilePath string, info *CoreInfo) error {
	cmd := exec.Command("file", corefilePath)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("file command failed: %w", err)
	}

	outputStr := string(output)

	// 查找 "from 'path'" 模式
	fromRe := regexp.MustCompile(`from '([^']+)'`)
	if matches := fromRe.FindStringSubmatch(outputStr); len(matches) >= 2 {
		info.ExecutablePath = matches[1]
		return nil
	}

	// 查找 "execfn: 'path'" 模式
	execfnRe := regexp.MustCompile(`execfn: '([^']+)'`)
	if matches := execfnRe.FindStringSubmatch(outputStr); len(matches) >= 2 {
		info.ExecutablePath = matches[1]
		return nil
	}

	return fmt.Errorf("could not extract executable path from file output: %s", outputStr)
}

// GetProcessNameFromPath 从可执行文件路径提取进程名
func GetProcessNameFromPath(execPath string) string {
	if execPath == "" {
		return ""
	}
	return filepath.Base(execPath)
}