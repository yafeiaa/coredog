package coreparser

import (
	"regexp"
	"strings"
	"testing"
)

func TestFileCommandOutputParsing(t *testing.T) {
	tests := []struct {
		name         string
		output       string
		expectedPath string
		shouldError  bool
	}{
		{
			name:         "from_pattern",
			output:       "core.bash.12345: from '/bin/bash'",
			expectedPath: "/bin/bash",
		},
		{
			name:         "execfn_pattern",
			output:       "core.python3.8901: execfn: '/usr/bin/python3'",
			expectedPath: "/usr/bin/python3",
		},
		{
			name:        "no_path",
			output:      "core.unknown.3333: ELF 64-bit LSB core file",
			shouldError: true,
		},
		{
			name:         "full_file_output",
			output:       "core.myapp.1234: ELF 64-bit LSB core file, x86-64, version 1 (SYSV), SVR4-style, from '/opt/myapp/bin/myapp --config /etc/myapp.conf', real uid: 1000",
			expectedPath: "/opt/myapp/bin/myapp --config /etc/myapp.conf",
		},
		{
			name:         "both_patterns",
			output:       "core.test.999: from '/usr/local/bin/test', execfn: '/usr/local/bin/test'",
			expectedPath: "/usr/local/bin/test",
		},
		{
			name:         "path_with_spaces",
			output:       "core.app.123: from '/path/to/my app'",
			expectedPath: "/path/to/my app",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, err := extractExecutablePathFromFileOutput(tt.output)
			if tt.shouldError {
				if err == nil {
					t.Errorf("expected error but got none, path=%s", path)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if path != tt.expectedPath {
					t.Errorf("expected path %q, got %q", tt.expectedPath, path)
				}
			}
		})
	}
}

// extractExecutablePathFromFileOutput 从 file 命令输出中提取可执行文件路径
func extractExecutablePathFromFileOutput(output string) (string, error) {
	// 查找 "from 'path'" 模式
	fromRe := regexp.MustCompile(`from '([^']+)'`)
	if matches := fromRe.FindStringSubmatch(output); len(matches) >= 2 {
		return matches[1], nil
	}

	// 查找 "execfn: 'path'" 模式
	execfnRe := regexp.MustCompile(`execfn: '([^']+)'`)
	if matches := execfnRe.FindStringSubmatch(output); len(matches) >= 2 {
		return matches[1], nil
	}

	return "", errNoPathFound
}

var errNoPathFound = &pathNotFoundError{}

type pathNotFoundError struct{}

func (e *pathNotFoundError) Error() string {
	return "could not extract executable path from file command output"
}

func TestGetProcessNameFromPath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/bin/bash", "bash"},
		{"/usr/bin/python3", "python3"},
		{"/opt/myapp/bin/myapp", "myapp"},
		{"", ""},
		{"simple", "simple"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := GetProcessNameFromPath(tt.path)
			if result != tt.expected {
				t.Errorf("GetProcessNameFromPath(%q) = %q, want %q", tt.path, result, tt.expected)
			}
		})
	}
}

func TestFileCommandOutputWithTooManyHeaders(t *testing.T) {
	tests := []struct {
		name           string
		output         string
		shouldFallback bool
	}{
		{
			name:           "too_many_headers",
			output:         "core.silo-server-0.dotnet.1.1767942737: ELF 64-bit LSB core file, x86-64, version 1 (SYSV), too many program headers (3293)",
			shouldFallback: true,
		},
		{
			name:           "normal_output",
			output:         "core.bash.12345: from '/bin/bash'",
			shouldFallback: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hasTooManyHeaders := regexp.MustCompile(`too many program headers`).MatchString(tt.output)
			if hasTooManyHeaders != tt.shouldFallback {
				t.Errorf("expected shouldFallback=%v, got hasTooManyHeaders=%v", tt.shouldFallback, hasTooManyHeaders)
			}
		})
	}
}

func TestReadelfOutputParsing(t *testing.T) {
	tests := []struct {
		name         string
		output       string
		expectedPath string
		shouldError  bool
	}{
		{
			name:         "psargs_single_line",
			output:       "CORE          NT_PRPSINFO\n    state: 0, sname: R\n    psargs: /usr/bin/dotnet /path/to/app.dll",
			expectedPath: "/usr/bin/dotnet",
		},
		{
			name:         "psargs_with_args",
			output:       "CORE          NT_PRPSINFO\n    psargs: /opt/myapp/bin/myapp --config /etc/myapp.conf",
			expectedPath: "/opt/myapp/bin/myapp",
		},
		{
			name:         "psargs_multiple_spaces",
			output:       "CORE          NT_PRPSINFO\n    psargs: /usr/bin/python3    /path/to/script.py",
			expectedPath: "/usr/bin/python3",
		},
		{
			name:        "no_psargs",
			output:      "CORE          NT_PRPSINFO\n    state: 0, sname: R\n    fname: dotnet",
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, err := extractExecutablePathFromReadelfOutput(tt.output)
			if tt.shouldError {
				if err == nil {
					t.Errorf("expected error but got none, path=%s", path)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if path != tt.expectedPath {
					t.Errorf("expected path %q, got %q", tt.expectedPath, path)
				}
			}
		})
	}
}

// extractExecutablePathFromReadelfOutput 从 readelf 输出中提取可执行文件路径（用于测试）
func extractExecutablePathFromReadelfOutput(output string) (string, error) {
	// 尝试匹配 psargs 行（包含完整命令行）
	psargsRe := regexp.MustCompile(`psargs:\s+([^\n]+)`)
	if matches := psargsRe.FindStringSubmatch(output); len(matches) >= 2 {
		// psargs 可能包含参数，只取第一个部分（可执行文件路径）
		psargsLine := strings.TrimSpace(matches[1])
		// 移除可能的换行符和多余空格
		psargsLine = regexp.MustCompile(`\s+`).ReplaceAllString(psargsLine, " ")
		args := strings.Fields(psargsLine)
		if len(args) > 0 {
			return args[0], nil
		}
	}
	return "", errNoPathFound
}
