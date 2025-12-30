package handler

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"
)

// CoredumpInfo contains information about the coredump file
type CoredumpInfo struct {
	FilePath       string
	FileURL        string
	FileName       string
	MD5            string
	FileSize       int64
	ExecutablePath string
}

// PodInfo contains information about the pod
type PodInfo struct {
	Name          string
	Namespace     string
	UID           string
	NodeIP        string
	Image         string
	ContainerName string
	IsLegacyPath  bool // 标记是否来自旧路径格式
}

// CustomHandler executes user-defined scripts for coredump processing
type CustomHandler struct {
	script  string
	timeout time.Duration
}

// NewCustomHandler creates a new CustomHandler instance
func NewCustomHandler(script string, timeoutSeconds int) *CustomHandler {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 300
	}
	return &CustomHandler{
		script:  script,
		timeout: time.Duration(timeoutSeconds) * time.Second,
	}
}

// Execute runs the custom script with coredump and pod information as environment variables
func (h *CustomHandler) Execute(ctx context.Context, coredump CoredumpInfo, pod PodInfo) error {
	if h.script == "" {
		return fmt.Errorf("custom handler script is empty")
	}

	// Create a temporary script file
	tmpDir := os.TempDir()
	scriptPath := filepath.Join(tmpDir, fmt.Sprintf("coredog-handler-%d.sh", time.Now().UnixNano()))

	if err := os.WriteFile(scriptPath, []byte(h.script), 0755); err != nil {
		return fmt.Errorf("failed to write script file: %w", err)
	}
	defer os.Remove(scriptPath)

	// Create context with timeout
	execCtx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	// Prepare command
	cmd := exec.CommandContext(execCtx, "/bin/bash", scriptPath)

	// Set environment variables
	// 注意：所有变量都会设置，即使值为空，脚本可以通过 -z 检查变量是否为空
	cmd.Env = append(os.Environ(),
		// Coredump info
		fmt.Sprintf("COREDUMP_FILE=%s", coredump.FilePath),
		fmt.Sprintf("COREDUMP_URL=%s", coredump.FileURL),
		fmt.Sprintf("COREDUMP_FILENAME=%s", coredump.FileName),
		fmt.Sprintf("COREDUMP_MD5=%s", coredump.MD5),
		fmt.Sprintf("COREDUMP_SIZE=%d", coredump.FileSize),
		fmt.Sprintf("COREDUMP_EXECUTABLE=%s", coredump.ExecutablePath),
		// Pod info (部分字段在旧路径格式下可能为空)
		fmt.Sprintf("POD_NAME=%s", pod.Name),
		fmt.Sprintf("POD_NAMESPACE=%s", pod.Namespace),
		fmt.Sprintf("POD_UID=%s", pod.UID),
		fmt.Sprintf("POD_NODE_IP=%s", pod.NodeIP),
		fmt.Sprintf("POD_IMAGE=%s", pod.Image),
		fmt.Sprintf("POD_CONTAINER=%s", pod.ContainerName),
		// Host info
		fmt.Sprintf("HOST_IP=%s", os.Getenv("HOST_IP")),
	)

	// Capture output
	output, err := cmd.CombinedOutput()
	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			logrus.Errorf("custom handler script timed out after %v, output: %s", h.timeout, string(output))
			return fmt.Errorf("script execution timed out after %v", h.timeout)
		}
		logrus.Errorf("custom handler script failed: %v, output: %s", err, string(output))
		return fmt.Errorf("script execution failed: %w", err)
	}

	logrus.Infof("custom handler script executed successfully, output: %s", string(output))
	return nil
}
