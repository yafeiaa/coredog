package config

import (
	"log"
	"os"
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
		Enabled           bool   `yaml:"enabled" env-default:"false"`
		Script            string `yaml:"script"`
		Timeout           int    `yaml:"timeout" env-default:"300"`
		SkipDefaultNotify bool   `yaml:"skipDefaultNotify" env-default:"true"`
		SkipCoreSight     bool   `yaml:"skipCoreSight" env-default:"true"`
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
	})
	return cfg
}
