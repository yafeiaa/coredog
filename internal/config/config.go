package config

import (
	"log"
	"os"
	"strings"
	"sync"

	"github.com/ilyakaznacheev/cleanenv"
)

var (
	onceCfg        sync.Once
	cfg            *Config
	defaultCfgPath = "/etc/config/coredog.yaml"
)

type Config struct {
	StorageConfig struct {
		Enabled                   bool   `yaml:"enabled" env-default:"true"`
		Protocol                  string `yaml:"protocol" env-default:"s3"`
		S3AccessKeyID             string `yaml:"s3AccesskeyID"`
		S3SecretAccessKey         string `yaml:"s3SecretAccessKey"`
		S3Region                  string `yaml:"s3Region"`
		S3Bucket                  string `yaml:"S3Bucket"`
		S3Endpoint                string `yaml:"S3Endpoint"`
		CFSMountPath              string `yaml:"CFSMountPath"`
		StoreDir                  string `yaml:"StoreDir"`
		PresignedURLExpireSeconds int    `yaml:"PresignedURLExpireSeconds"`
		DeleteLocalCorefile       bool   `yaml:"deleteLocalCorefile"`
	} `yaml:"StorageConfig"`
	Gc          bool   `yaml:"gc" env-default:"false"`
	GcType      string `yaml:"gc_type" env-default:"rm"`
	CorefileDir string `yaml:"CorefileDir"`

	// CoredogPathBase 是 webhook 注入的宿主机路径基座：
	//   <CoredogPathBase>/<namespace>/<pod-name>/<container-name>/
	// 留空时 webhook 使用默认值 /data/coredog-system/dumps（见 webhook 包常量）。
	// ⚠️ 必须与 watcher DaemonSet 挂载的 corefileVolume.hostPath.path 一致，
	//    否则 core dump 会落在挂载点之外，watcher 无法感知（chart 会自动注入保持一致）。
	// ⚠️ 不要加 env-default 标签：cleanenv 会用它覆盖 YAML 显式值（见 commit 15a4583）。
	CoredogPathBase string `yaml:"CoredogPathBase"`

	// Notice configuration (merged from controller)
	NoticeChannel []struct {
		Chan       string `yaml:"chan"`
		Webhookurl string `yaml:"webhookurl"`
		Keyword    string `yaml:"keyword"`
	} `yaml:"NoticeChannel"`
	MessageTemplate string            `yaml:"messageTemplate"`
	MessageLabels   map[string]string `yaml:"messageLabels"`

	// CoreSight integration configuration
	CoreSight struct {
		Enabled bool   `yaml:"enabled" env-default:"false"`
		NatsURL string `yaml:"natsUrl" env:"CORESIGHT_NATS_URL"`
		Token   string `yaml:"token" env:"CORESIGHT_TOKEN"`
	} `yaml:"CoreSight"`

	// CustomHandler configuration for executing custom scripts
	CustomHandler struct {
		Enabled           bool   `yaml:"enabled"`
		Script            string `yaml:"script"`
		Timeout           int    `yaml:"timeout"`
		SkipDefaultNotify bool   `yaml:"skipDefaultNotify"`
		SkipCoreSight     bool   `yaml:"skipCoreSight"`
		// TriggerOnAllFiles controls which files trigger the custom script.
		// false (default): only files successfully parsed as ELF core dumps
		// (coreInfo != nil) trigger the script — the original behavior.
		// true: every newly detected file triggers the script, including
		// non-ELF files (e.g. application-level crash dumps such as sglang's
		// crash_dump_*.pkl). For non-ELF files the handler still receives
		// file path/size/MD5; ExecutablePath stays empty.
		TriggerOnAllFiles bool `yaml:"triggerOnAllFiles"`
	} `yaml:"CustomHandler"`
}

func Get() *Config {
	onceCfg.Do(func() {
		cfg = &Config{}
		cfgPath := os.Getenv("CONFIG_PATH")
		if cfgPath == "" {
			cfgPath = defaultCfgPath
		}
		if err := cleanenv.ReadConfig(cfgPath, cfg); err != nil {
			log.Fatal(err)
		}
		if cfg.CorefileDir == "" {
			cfg.CorefileDir = "/corefile"
		}
		// CoredogPathBase 留空时不兜底：webhook 侧使用包默认常量，
		// 这样老配置文件（无此字段）行为不变。
		cfg.CoredogPathBase = strings.TrimSpace(cfg.CoredogPathBase)
	})
	return cfg
}
