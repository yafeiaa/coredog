package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	cfgpkg "github.com/DomineCore/coredog/internal/config"
	"github.com/DomineCore/coredog/internal/notice"
	"github.com/DomineCore/coredog/internal/podresolver"
	"github.com/DomineCore/coredog/internal/reporter"
	"github.com/DomineCore/coredog/internal/store"
	"github.com/DomineCore/coredog/internal/watcher"
	"github.com/sirupsen/logrus"
)

func getHostIP() string { return os.Getenv("HOST_IP") }

func buildNotifyMessage(cfg *cfgpkg.Config, corefilePath, url string, pod podresolver.PodInfo) string {
	msg := cfg.MessageTemplate
	for k, v := range cfg.MessageLabels {
		msg = strings.ReplaceAll(msg, "{"+k+"}", v)
	}
	_, filename := filepath.Split(corefilePath)
	msg = strings.ReplaceAll(msg, "{corefile.path}", corefilePath)
	msg = strings.ReplaceAll(msg, "{corefile.filename}", filename)
	msg = strings.ReplaceAll(msg, "{corefile.url}", url)

	// 如果 Pod 名称为空，使用 UID 或 "unknown"
	podName := pod.Name
	if podName == "" && pod.UID != "" {
		podName = "pod-" + pod.UID[:8] + "..." // 显示 UID 前8位
	} else if podName == "" {
		podName = "unknown"
	}

	msg = strings.ReplaceAll(msg, "{pod.name}", podName)
	msg = strings.ReplaceAll(msg, "{pod.namespace}", pod.Namespace)
	msg = strings.ReplaceAll(msg, "{pod.uid}", pod.UID)
	msg = strings.ReplaceAll(msg, "{pod.node}", pod.Node)
	msg = strings.ReplaceAll(msg, "{host.ip}", getHostIP())
	return msg
}

func notify(cfg *cfgpkg.Config, corefilePath, url string, pod podresolver.PodInfo) {
	for _, ch := range cfg.NoticeChannel {
		if ch.Keyword != "" && !strings.Contains(corefilePath, ch.Keyword) {
			continue
		}
		msg := buildNotifyMessage(cfg, corefilePath, url, pod)
		switch ch.Chan {
		case "wechat":
			n := notice.NewWechatWebhookMsg(ch.Webhookurl)
			n.Notice(msg)
		case "slack":
			n := notice.NewSlackWebhookMsg(ch.Webhookurl)
			n.Notice(msg)
		default:
			logrus.Warnf("unsupported notice channel: %s", ch.Chan)
		}
	}
}

// Run starts the corefile watcher agent
func Run() {
	wcfg := cfgpkg.Get()
	receiver := make(chan string)
	w := watcher.NewFileWatcher(receiver)
	if err := w.Watch(wcfg.CorefileDir); err != nil {
		logrus.Fatal(err)
	}
	storeClient, err := store.NewStore(
		wcfg.StorageConfig.Protocol,
		wcfg.StorageConfig.S3Region,
		wcfg.StorageConfig.S3AccessKeyID,
		wcfg.StorageConfig.S3SecretAccessKey,
		wcfg.StorageConfig.S3Bucket,
		wcfg.StorageConfig.S3Endpoint,
		wcfg.StorageConfig.CFSMountPath,
		wcfg.StorageConfig.StoreDir,
		wcfg.StorageConfig.PresignedURLExpireSeconds,
	)
	if err != nil {
		logrus.Fatal(err)
	}

	// 初始化 CoreSight reporter
	var csReporter *reporter.Reporter
	if wcfg.CoreSight.Enabled {
		csReporter = reporter.NewReporterWithToken(wcfg.CoreSight.NatsURL, wcfg.CoreSight.Token)
		if csReporter != nil {
			logrus.Infof("CoreSight integration enabled: %s", wcfg.CoreSight.NatsURL)
		}
	}

	ccfg := wcfg
	for corefilePath := range receiver {
		url, err := storeClient.Upload(context.Background(), corefilePath)
		if err != nil {
			logrus.Errorf("store a corefile error:%v", err)
			continue
		}

		// 上传成功后，根据配置清理本地文件
		if wcfg.StorageConfig.DeleteLocalCorefile {
			if wcfg.Gc && wcfg.GcType == "truncate" {
				// 如果明确配置了 truncate，则清空文件而不是删除
				if err := os.Truncate(corefilePath, 0); err != nil {
					logrus.Errorf("failed to truncate corefile %s: %v", corefilePath, err)
				} else {
					logrus.Infof("truncated local corefile: %s", corefilePath)
				}
			} else {
				// 默认删除文件
				if err := os.Remove(corefilePath); err != nil {
					logrus.Errorf("failed to remove corefile %s: %v", corefilePath, err)
				} else {
					logrus.Infof("deleted local corefile: %s", corefilePath)
				}
			}
		}

		pod := podresolver.Resolve(corefilePath, strings.ToLower(strings.TrimSpace(os.Getenv("KUBE_LOOKUP"))) == "true")
		notify(ccfg, corefilePath, url, pod)

		// 上报事件到 CoreSight
		if csReporter != nil {
			_, filename := filepath.Split(corefilePath)
			data := &reporter.CoredumpUploadedData{
				CoredumpID:     uint64(0), // 由 CoreSight 自动分配
				FileURL:        url,
				FileName:       filename,
				ExecutablePath: pod.Name, // 使用 pod 名称作为可执行文件路径示例
				FileSize:       0,        // 可根据需要从文件信息获取
				Image:          os.Getenv("POD_IMAGE"),
				Timestamp:      time.Now().UTC().Format(time.RFC3339),
				PodName:        pod.Name,
				PodNamespace:   pod.Namespace,
				NodeName:       pod.Node,
			}

			// 获取文件大小
			if fi, err := os.Stat(corefilePath); err == nil {
				data.FileSize = fi.Size()
			}

			if err := csReporter.ReportCoredumpUploaded(context.Background(), data); err != nil {
				logrus.Errorf("failed to report coredump to CoreSight: %v", err)
				// 不中断流程，继续处理其他任务
			}
		}
	}
}
