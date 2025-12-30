package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	cfgpkg "github.com/DomineCore/coredog/internal/config"
	"github.com/DomineCore/coredog/internal/coreparser"
	"github.com/DomineCore/coredog/internal/handler"
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
	msg = strings.ReplaceAll(msg, "{pod.node}", pod.NodeIP)
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

	// 初始化自定义处理器
	var customHandler *handler.CustomHandler
	if wcfg.CustomHandler.Enabled {
		customHandler = handler.NewCustomHandler(wcfg.CustomHandler.Script, wcfg.CustomHandler.Timeout)
		logrus.Infof("CustomHandler enabled: skipDefaultNotify=%v, skipCoreSight=%v, timeout=%ds",
			wcfg.CustomHandler.SkipDefaultNotify, wcfg.CustomHandler.SkipCoreSight, wcfg.CustomHandler.Timeout)
	}

	ccfg := wcfg
	for corefilePath := range receiver {
		// 解析 core 文件获取可执行文件路径（必须成功）
		coreInfo, err := coreparser.ParseCoreFile(corefilePath)
		if err != nil {
			logrus.Errorf("failed to parse core file %s: %v", corefilePath, err)
			continue // file 命令失败，跳过此文件
		}

		url, err := storeClient.Upload(context.Background(), corefilePath)
		if err != nil {
			logrus.Errorf("store a corefile error:%v", err)
			continue
		}

		// 上传成功后，根据配置清理本地文件
		if wcfg.StorageConfig.DeleteLocalCorefile {
			if wcfg.Gc && wcfg.GcType == "truncate" {
				if err := os.Truncate(corefilePath, 0); err != nil {
					logrus.Errorf("failed to truncate corefile %s: %v", corefilePath, err)
				} else {
					logrus.Infof("truncated local corefile: %s", corefilePath)
				}
			} else {
				if err := os.Remove(corefilePath); err != nil {
					logrus.Errorf("failed to remove corefile %s: %v", corefilePath, err)
				} else {
					logrus.Infof("deleted local corefile: %s", corefilePath)
				}
			}
		}

		// enableLookup 默认为 true，除非明确设置为 false
		enableLookup := strings.ToLower(strings.TrimSpace(os.Getenv("KUBE_LOOKUP"))) != "false"
		pod := podresolver.Resolve(corefilePath, enableLookup)

		// 判断是否跳过默认通知和 CoreSight 上报
		skipNotify := false
		skipCoreSight := false

		// 执行自定义处理器
		if customHandler != nil {
			_, filename := filepath.Split(corefilePath)
			coredumpInfo := handler.CoredumpInfo{
				FilePath:       corefilePath,
				FileURL:        url,
				FileName:       filename,
				MD5:            coreInfo.MD5,
				FileSize:       coreInfo.FileSize,
				ExecutablePath: coreInfo.ExecutablePath,
			}
			podInfo := handler.PodInfo{
				Name:          pod.Name,
				Namespace:     pod.Namespace,
				UID:           pod.UID,
				NodeIP:        pod.NodeIP,
				Image:         pod.Image,
				ContainerName: pod.ContainerName,
				IsLegacyPath:  pod.IsLegacyPath,
			}
			if err := customHandler.Execute(context.Background(), coredumpInfo, podInfo); err != nil {
				logrus.Errorf("custom handler execution failed: %v", err)
			}

			skipNotify = wcfg.CustomHandler.SkipDefaultNotify
			skipCoreSight = wcfg.CustomHandler.SkipCoreSight
		}

		// 发送通知
		if !skipNotify {
			notify(ccfg, corefilePath, url, pod)
		}

		// 跳过 CoreSight 上报
		if skipCoreSight {
			continue
		}

		// 上报事件到 CoreSight
		if csReporter != nil {
			// 检查是否为旧路径格式，旧格式不上报到 CoreSight
			if pod.IsLegacyPath {
				logrus.Warnf("detected legacy path format for corefile: %s. Please upgrade to the new path structure: /data/coredog-system/dumps/<namespace>/<pod-name>/<container-name>/core.xxx. Skipping CoreSight reporting.", corefilePath)
				continue
			}

			_, filename := filepath.Split(corefilePath)

			// 验证必要字段，有异常则不上报
			var validationErrors []string
			if coreInfo.ExecutablePath == "" {
				validationErrors = append(validationErrors, "executable_path is empty")
			}
			if coreInfo.MD5 == "" {
				validationErrors = append(validationErrors, "md5 is empty")
			}
			if pod.Name == "" {
				validationErrors = append(validationErrors, "pod_name is empty")
			}
			if strings.HasPrefix(pod.Name, "pod-") && len(pod.Name) == 12 {
				// pod-xxxxxxxx 格式说明是从 admission-uid 生成的假名称
				validationErrors = append(validationErrors, "pod_name is generated from admission-uid (pod not found)")
			}
			if pod.Namespace == "" {
				validationErrors = append(validationErrors, "pod_namespace is empty")
			}
			if pod.Image == "" {
				validationErrors = append(validationErrors, "image is empty")
			}
			if pod.NodeIP == "" {
				validationErrors = append(validationErrors, "node_ip is empty")
			}

			if len(validationErrors) > 0 {
				logrus.Errorf("skip reporting to CoreSight due to missing fields: %v, file=%s", validationErrors, corefilePath)
				continue
			}

			data := &reporter.CoredumpUploadedData{
				FileURL:        url,
				FileName:       filename,
				ExecutablePath: coreInfo.ExecutablePath,
				FileSize:       coreInfo.FileSize,
				MD5:            coreInfo.MD5,
				Image:          pod.Image,
				Timestamp:      time.Now().UTC().Format(time.RFC3339),
				PodName:        pod.Name,
				PodNamespace:   pod.Namespace,
				NodeIP:         pod.NodeIP,
			}

			if err := csReporter.ReportCoredumpUploaded(context.Background(), data); err != nil {
				logrus.Errorf("failed to report coredump to CoreSight: %v", err)
			} else {
				logrus.Infof("CoreSight event reported: executable=%s, size=%d, md5=%s",
					coreInfo.ExecutablePath, coreInfo.FileSize, coreInfo.MD5)
			}
		}
	}
}
