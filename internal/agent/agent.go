package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	cfgpkg "github.com/DomineCore/coredog/internal/config"
	"github.com/DomineCore/coredog/internal/notice"
	"github.com/DomineCore/coredog/internal/podresolver"
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
	msg = strings.ReplaceAll(msg, "{pod.name}", pod.Name)
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
	storeClient, err := store.NewS3Store(
		wcfg.StorageConfig.S3Region,
		wcfg.StorageConfig.S3AccessKeyID,
		wcfg.StorageConfig.S3SecretAccessKey,
		wcfg.StorageConfig.S3Bucket,
		wcfg.StorageConfig.S3Endpoint,
		wcfg.StorageConfig.StoreDir,
		wcfg.StorageConfig.PresignedURLExpireSeconds,
	)
	if err != nil {
		logrus.Fatal(err)
	}
	ccfg := wcfg
	for corefilePath := range receiver {
		url, err := storeClient.Upload(context.Background(), corefilePath)
		if err != nil {
			logrus.Errorf("store a corefile error:%v", err)
			continue
		}
		if wcfg.Gc && wcfg.GcType == "rm" {
			_ = os.Remove(corefilePath)
		} else if wcfg.Gc && wcfg.GcType == "truncate" {
			_ = os.Truncate(corefilePath, 0)
		}
		pod := podresolver.Resolve(corefilePath, strings.ToLower(strings.TrimSpace(os.Getenv("KUBE_LOOKUP"))) == "true")
		notify(ccfg, corefilePath, url, pod)
	}
}
