package coreparser

import (
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
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
// 如果 file 命令因为程序头过多而失败，会尝试使用 readelf 作为备选方案
func parseWithFileCommand(corefilePath string, info *CoreInfo) error {
	// 首先尝试使用 file 命令，增加程序头限制以处理大型 core 文件
	// -Pelf_phnum=10000 允许处理最多 10000 个程序头
	cmd := exec.Command("file", "-Pelf_phnum=10000", corefilePath)
	output, err := cmd.Output()
	if err != nil {
		// file 命令失败，尝试使用 readelf
		return parseWithReadelf(corefilePath, info)
	}

	outputStr := string(output)

	// 检查是否包含 "too many program headers" 错误
	if regexp.MustCompile(`too many program headers`).MatchString(outputStr) {
		// 即使增加了限制仍然失败，尝试使用 readelf
		return parseWithReadelf(corefilePath, info)
	}

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

	// 无法从 file 输出中提取路径，尝试使用 readelf
	return parseWithReadelf(corefilePath, info)
}

// parseWithReadelf 使用 readelf 命令获取 core 文件中的可执行文件路径
// readelf 从 ELF 文件的 notes 段中读取信息，通常更可靠
func parseWithReadelf(corefilePath string, info *CoreInfo) error {
	// 使用 readelf -n 读取 notes 段，其中包含可执行文件路径
	cmd := exec.Command("readelf", "-n", corefilePath)
	output, err := cmd.Output()
	if err != nil {
		// readelf 命令失败，尝试使用 strings 作为最后的备选方案
		return parseWithStrings(corefilePath, info)
	}

	outputStr := string(output)

	// 在 readelf 输出中查找可执行文件路径
	// readelf -n 输出格式示例：
	//   CORE          NT_PRPSINFO
	//     state: 0, sname: R, zomb: 0, nice: 0, flag: 0x0000000000000000
	//     uid: 0, gid: 0, pid: 12345, ppid: 1, pgrp: 12345, sid: 12345
	//     fname: dotnet
	//     psargs: /usr/bin/dotnet /path/to/app.dll
	//
	// 或者可能跨多行：
	//     psargs: /usr/bin/dotnet
	//             /path/to/app.dll

	// 尝试匹配 psargs 行（包含完整命令行）
	// psargs 可能跨多行，需要处理换行情况
	psargsRe := regexp.MustCompile(`psargs:\s+([^\n]+)`)
	if matches := psargsRe.FindStringSubmatch(outputStr); len(matches) >= 2 {
		// psargs 可能包含参数，只取第一个部分（可执行文件路径）
		// 处理可能的换行和空格
		psargsLine := strings.TrimSpace(matches[1])
		// 移除可能的换行符和多余空格
		psargsLine = regexp.MustCompile(`\s+`).ReplaceAllString(psargsLine, " ")
		args := strings.Fields(psargsLine)
		if len(args) > 0 {
			info.ExecutablePath = args[0]
			return nil
		}
	}

	// readelf 成功执行但无法提取路径，尝试使用 strings 命令作为最后的备选方案
	return parseWithStrings(corefilePath, info)
}

// parseWithStrings 使用 strings 命令从 core 文件中提取可执行文件路径
// 这是一个最后的备选方案
func parseWithStrings(corefilePath string, info *CoreInfo) error {
	// 使用 strings 命令提取可打印字符串，然后查找可执行文件路径
	// -n 8 表示只显示长度至少为 8 的字符串，这样可以过滤掉大部分噪音
	cmd := exec.Command("strings", "-n", "8", corefilePath)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("strings command failed: %w", err)
	}

	outputStr := string(output)
	lines := strings.Split(outputStr, "\n")

	// 查找看起来像可执行文件路径的字符串
	// 优先查找以 / 开头的绝对路径，且不包含 .so（共享库）
	executableRe := regexp.MustCompile(`^/(?:usr|opt|bin|sbin|home|var|tmp)/[^\s]+$`)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if executableRe.MatchString(line) {
			// 排除明显的共享库路径
			if strings.Contains(line, ".so") || strings.Contains(line, "/lib/") {
				continue
			}
			// 检查文件是否存在（在容器环境中可能不可用，但值得尝试）
			// 如果路径看起来合理，就使用它
			if strings.HasPrefix(line, "/usr/bin/") || strings.HasPrefix(line, "/usr/local/bin/") ||
				strings.HasPrefix(line, "/opt/") || strings.HasPrefix(line, "/app/") {
				info.ExecutablePath = line
				return nil
			}
		}
	}

	return fmt.Errorf("could not extract executable path using readelf or strings")
}

// GetProcessNameFromPath 从可执行文件路径提取进程名
func GetProcessNameFromPath(execPath string) string {
	if execPath == "" {
		return ""
	}
	return filepath.Base(execPath)
}
