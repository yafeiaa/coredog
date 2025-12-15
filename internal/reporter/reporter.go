package reporter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/nats-io/nats.go"
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

// Reporter 负责向 CoreSight 上报事件（通过 NATS）
type Reporter struct {
	natsConn *nats.Conn
}

// NewReporter 创建新的 reporter（连接到 NATS）
func NewReporter(natsURL string) *Reporter {
	if natsURL == "" {
		logrus.Warn("CoreSight NATS URL is not configured, event reporting disabled")
		return nil
	}

	nc, err := nats.Connect(natsURL)
	if err != nil {
		logrus.Warnf("Failed to connect to NATS at %s: %v", natsURL, err)
		return nil
	}

	logrus.Infof("[CoreSight] Connected to NATS: %s", natsURL)
	return &Reporter{
		natsConn: nc,
	}
}

// Close 关闭 reporter 连接
func (r *Reporter) Close() error {
	if r != nil && r.natsConn != nil {
		r.natsConn.Close()
		logrus.Info("[CoreSight] Closed NATS connection")
	}
	return nil
}

// ReportCoredumpUploaded 上报 coredump 上传事件到 CoreSight
func (r *Reporter) ReportCoredumpUploaded(ctx context.Context, data *CoredumpUploadedData) error {
	if r == nil || r.natsConn == nil {
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

	// 发布到 NATS
	if err := r.natsConn.Publish(event.Type, body); err != nil {
		return fmt.Errorf("failed to publish event to NATS: %w", err)
	}

	// 刷新缓冲区，确保消息被发送
	if err := r.natsConn.Flush(); err != nil {
		return fmt.Errorf("failed to flush NATS connection: %w", err)
	}

	logrus.Infof(
		"[CoreSight] Successfully reported coredump uploaded event: %s (event_id: %s)",
		data.FileURL,
		eventID,
	)

	return nil
}
