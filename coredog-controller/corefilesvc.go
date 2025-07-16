package coredogcontroller

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/DomineCore/coredog/internal/notice"
	"github.com/DomineCore/coredog/pb"
	"github.com/sirupsen/logrus"
)

type CorefileService struct {
}

func (s *CorefileService) Sub(ctx context.Context, r *pb.Corefile) (*pb.Corefile, error) {
	logrus.Infof("recevier a newfile:%s from host:%s", r.Filepath, r.Ip)
	cfg = getCfg()
	for _, noticeChan := range cfg.NoticeChannel {
		corefilePath, corefilename := filepath.Split(r.Filepath)
		msg := buildMessage(cfg.MessageTemplate, corefilePath, corefilename, cfg.MessageLabels, r.Url)
		// 如果noticeChan.Keyword不为空，则当文件中包含该关键字时，发送到该channel，其他情况不发送到该channel
		if noticeChan.Keyword != "" && !strings.Contains(r.Filepath, noticeChan.Keyword) {
			continue
		}
		if noticeChan.Chan == "wechat" {
			c := notice.NewWechatWebhookMsg(noticeChan.Webhookurl)
			c.Notice(msg)
		} else if noticeChan.Chan == "slack" {
			c := notice.NewSlackWebhookMsg(noticeChan.Webhookurl)
			c.Notice(msg)
		} else {
			logrus.Warnf("unsupported notice channel:%s", noticeChan.Chan)
		}
	}

	return &pb.Corefile{}, nil
}

func buildMessage(template string, corefilePath, corefilename string, labels map[string]string, url string) string {
	// 1 replace the labels into template
	msg := template
	for key, val := range labels {
		msg = strings.ReplaceAll(msg, fmt.Sprintf("{%v}", key), val)
	}
	msg = strings.ReplaceAll(msg, CorefilePath, corefilePath)
	msg = strings.ReplaceAll(msg, CorefileName, corefilename)
	msg = strings.ReplaceAll(msg, CorefileUrl, url)
	return msg
}
