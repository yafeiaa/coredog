package coredogcontroller

import (
	"log"
	"os"
	"sync"

	"github.com/ilyakaznacheev/cleanenv"
)

var (
	cfg              *Config
	onceCfg          sync.Once
	DEFAULT_CFG_PATH = "/etc/config/controller.yaml"
)

type Config struct {
	NoticeChannel []struct {
		// supported: wechat，slack
		Chan       string `yaml:"chan"`
		Webhookurl string `yaml:"webhookurl"`
		// 如果keyword不为空，则当文件中包含该关键字时，发送到该channel，其他情况不发送到该channel
		Keyword string `yaml:"keyword"`
	} `yaml:"NoticeChannel"`
	// example
	// support to reference to labels and corefile's name parameters
	// example
	// cluster: {label.cluster}
	// inner variable: {corefile.filename}
	MessageTemplate string `yaml:"messageTemplate"`
	// example
	// example
	// cluster: cluster-a
	// env: sigapore
	MessageLabels map[string]string `yaml:"messageLabels"`
	// if set can use inner variable yet. such as:
	// pid：{corefile.p}
	// uid：{corefile.u}
	// gid：{corefile.g}
	// signal: {corefile.s}
	// timestamp: {corefile.t}
	// host(pod): {corefile.h}
	// exe name: {corefile.e}
	// exe path: {corefile.E}
	// CorePattern string `yaml:"core_pattern"`
}

func getCfg() *Config {
	onceCfg.Do(func() {
		cfg = &Config{}
		cfgPath := os.Getenv("CONFIG_CONTROLLER_PATH")
		if cfgPath == "" {
			cfgPath = DEFAULT_CFG_PATH
		}
		if err := cleanenv.ReadConfig(cfgPath, cfg); err != nil {
			log.Fatal(err)
		}
	})
	return cfg
}
