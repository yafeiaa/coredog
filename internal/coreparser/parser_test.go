package coreparser

import (
	"regexp"
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
