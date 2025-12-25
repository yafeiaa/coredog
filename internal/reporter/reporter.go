package reporter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/sirupsen/logrus"
)

// CloudEvent 遵循 CloudEvents 1.0 规范
type CloudEvent struct {
	SpecVersion     string                 `json:"specversion"`
	Type            string                 `json:"type"`
	Source          string                 `json:"source"`
	ID              string                 `json:"id"`
	Time            string                 `json:"time"`
	DataContentType string                 `json:"datacontenttype"`
	Token           string                 `json:"token,omitempty"`
	Data            map[string]interface{} `json:"data"`
}

// CoredumpUploadedData 上传成功的 core dump 数据
type CoredumpUploadedData struct {
	CoredumpID     uint64 `json:"coredump_id"`
	FileURL        string `json:"file_url"`
	FileName       string `json:"file_name"`
	ExecutablePath string `json:"executable_path"`
	FileSize       int64  `json:"file_size"`
	MD5            string `json:"md5,omitempty"`
	Image          string `json:"image,omitempty"`
	Timestamp      string `json:"timestamp"`
	PodName        string `json:"pod_name,omitempty"`
	PodNamespace   string `json:"pod_namespace,omitempty"`
	NodeName       string `json:"node_name,omitempty"`
}

// Reporter 负责向 CoreSight 上报事件（通过 HTTP API）
type Reporter struct {
	httpClient *http.Client
	apiURL     string
	token      string
}

// NewReporter 创建新的 reporter（使用 HTTP API）
func NewReporter(apiURL string) *Reporter {
	return NewReporterWithToken(apiURL, "")
}

// NewReporterWithToken 创建新的 reporter（使用 HTTP API，并设置 token）
func NewReporterWithToken(apiURL string, token string) *Reporter {
	if apiURL == "" {
		logrus.Warn("CoreSight API URL is not configured, event reporting disabled")
		return nil
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	logrus.Infof("[CoreSight] Configured HTTP reporter: %s", apiURL)
	return &Reporter{
		httpClient: client,
		apiURL:     apiURL,
		token:      token,
	}
}

// Close 关闭 reporter 连接
func (r *Reporter) Close() error {
	if r != nil && r.httpClient != nil {
		logrus.Info("[CoreSight] HTTP reporter closed")
	}
	return nil
}

// ReportCoredumpUploaded 上报 coredump 上传事件到 CoreSight
func (r *Reporter) ReportCoredumpUploaded(ctx context.Context, data *CoredumpUploadedData) error {
	if r == nil || r.httpClient == nil {
		return nil // 如果 reporter 未配置，则忽略
	}

	// 生成事件 ID
	eventID := fmt.Sprintf("coredog-%d-%d", time.Now().Unix(), os.Getpid())

	// 构建 CloudEvents 事件
	event := &CloudEvent{
		SpecVersion:     "1.0",
		Type:            "coredog.coredump.uploaded",
		Source:          "coredog-agent",
		ID:              eventID,
		Time:            time.Now().UTC().Format(time.RFC3339),
		DataContentType: "application/json",
		Token:           r.token,
		Data: map[string]interface{}{
			"coredump_id":     data.CoredumpID,
			"file_url":        data.FileURL,
			"file_name":       data.FileName,
			"executable_path": data.ExecutablePath,
			"file_size":       data.FileSize,
			"md5":             data.MD5,
			"image":           data.Image,
			"timestamp":       data.Timestamp,
			"pod_name":        data.PodName,
			"pod_namespace":   data.PodNamespace,
			"node_name":       data.NodeName,
		},
	}

	// 序列化事件
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	// 创建 HTTP 请求
	req, err := http.NewRequestWithContext(ctx, "POST", r.apiURL, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}

	// 发送请求
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send HTTP request to %s: %w", r.apiURL, err)
	}
	defer resp.Body.Close()

	// 检查响应状态
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP request failed with status %d: %s", resp.StatusCode, resp.Status)
	}

	logrus.Infof(
		"[CoreSight] Successfully reported coredump uploaded event: %s (event_id: %s, status: %d)",
		data.FileURL,
		eventID,
		resp.StatusCode,
	)

	return nil
}
