package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

type AppConfig struct {
	Env           string `mapstructure:"env"`
	Port          int    `mapstructure:"port"`
	VerifyBaseURL string `mapstructure:"verify_base_url"`
	JWTSecret     string `mapstructure:"jwt_secret"`
}

type DatabaseConfig struct {
	DSN                string `mapstructure:"dsn"`
	MaxOpenConns       int    `mapstructure:"max_open_conns"`
	MaxIdleConns       int    `mapstructure:"max_idle_conns"`
	ConnMaxLifetimeSec int    `mapstructure:"conn_max_lifetime_sec"`
}

type NCANodeConfig struct {
	URL        string `mapstructure:"url"`
	TimeoutSec int    `mapstructure:"timeout_sec"`
}

type StorageConfig struct {
	Endpoint     string `mapstructure:"endpoint"`
	Region       string `mapstructure:"region"`
	Bucket       string `mapstructure:"bucket"`
	AccessKey    string `mapstructure:"access_key"`
	SecretKey    string `mapstructure:"secret_key"`
	UsePathStyle bool   `mapstructure:"use_path_style"`

	// Source-S3 — внешний бакет клиента (Lovable), где лежат исходные PDF.
	// Если поля пустые — используется основной клиент (один бакет на всё).
	SourceEndpoint     string `mapstructure:"source_endpoint"`
	SourceRegion       string `mapstructure:"source_region"`
	SourceAccessKey    string `mapstructure:"source_access_key"`
	SourceSecretKey    string `mapstructure:"source_secret_key"`
	SourceUsePathStyle bool   `mapstructure:"source_use_path_style"`
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type RabbitMQConfig struct {
	URL           string `mapstructure:"url"`
	WebhookQueue  string `mapstructure:"webhook_queue"`
	EventExchange string `mapstructure:"event_exchange"`
	PrefetchCount int    `mapstructure:"prefetch_count"`
}

type LogConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

type RateLimitConfig struct {
	VerifyPerMinute int `mapstructure:"verify_per_minute"`
	APIPerMinute    int `mapstructure:"api_per_minute"`
}

type ApplicationsConfig struct {
	FetchIntervalSec   int `mapstructure:"fetch_interval_sec"`
	FetchBatchSize     int `mapstructure:"fetch_batch_size"`
	MaxFetchRetries    int `mapstructure:"max_fetch_retries"`
	WebhookIntervalSec int `mapstructure:"webhook_interval_sec"`
	WebhookMaxAttempts int `mapstructure:"webhook_max_attempts"`
}

type SigningConfig struct {
	SessionTTLSec             int    `mapstructure:"session_ttl_sec"`
	FetchTimeoutSec           int    `mapstructure:"fetch_timeout_sec"`
	UploadMaxAttempts         int    `mapstructure:"upload_max_attempts"`
	UploadBackoffInitialSec   int    `mapstructure:"upload_backoff_initial_sec"`
	UploadBackoffMultiplier   int    `mapstructure:"upload_backoff_multiplier"`
	WebhookTimeoutSec         int    `mapstructure:"webhook_timeout_sec"`
	WebhookMaxAttempts        int    `mapstructure:"webhook_max_attempts"`
	CleanupIntervalSec        int    `mapstructure:"cleanup_interval_sec"`
	CacheTTLSec               int    `mapstructure:"cache_ttl_sec"`
	CacheBucket               string `mapstructure:"cache_bucket"`
	SignedBucket              string `mapstructure:"signed_bucket"`
	CMSBucket                 string `mapstructure:"cms_bucket"`
	// PostprocessTickIntervalSec — период тика PostprocessWorker (QR-штамп +
	// Лист подписей + upload клиенту в фоне после /sign/complete). По
	// умолчанию 3с — короткий интервал, т.к. это на пользовательском пути
	// восприятия скорости подписания. См. план: synthetic-launching-blanket.md.
	PostprocessTickIntervalSec int `mapstructure:"postprocess_tick_interval_sec"`
	// MaxPostprocessAttempts — потолок попыток перед терминальным
	// post_process_failed (по умолчанию 5).
	MaxPostprocessAttempts int `mapstructure:"max_postprocess_attempts"`
}

type VerificationConfig struct {
	// Enabled включает фоновый verification-воркер. По умолчанию false — нужно
	// включать явно через PKI_VERIFICATION_ENABLED=true (или yaml).
	Enabled bool `mapstructure:"enabled"`
	// AllowedBuckets — whitelist S3-бакетов, к которым воркер может HEAD'ить.
	// Список через запятую, ENV PKI_ALLOWED_S3_BUCKETS. Пустой → no whitelist.
	AllowedBuckets []string `mapstructure:"allowed_buckets"`
	// InitialDelaySec — задержка между signed_at и первой проверкой (по умолчанию 60).
	InitialDelaySec int `mapstructure:"initial_delay_sec"`
	// TickIntervalSec — период тика воркера (по умолчанию 5).
	TickIntervalSec int `mapstructure:"tick_interval_sec"`
	// BatchSize — макс. кол-во документов на тик (по умолчанию 50).
	BatchSize int `mapstructure:"batch_size"`
	// DeadlineHours — после signed_at + DeadlineHours воркер фиксирует unavailable (по умолчанию 24).
	DeadlineHours int `mapstructure:"deadline_hours"`
}

type Config struct {
	App          AppConfig          `mapstructure:"app"`
	Database     DatabaseConfig     `mapstructure:"database"`
	NCANode      NCANodeConfig      `mapstructure:"ncanode"`
	Storage      StorageConfig      `mapstructure:"storage"`
	Redis        RedisConfig        `mapstructure:"redis"`
	RabbitMQ     RabbitMQConfig     `mapstructure:"rabbitmq"`
	Log          LogConfig          `mapstructure:"log"`
	RateLimit    RateLimitConfig    `mapstructure:"rate_limit"`
	Applications ApplicationsConfig `mapstructure:"applications"`
	Signing      SigningConfig      `mapstructure:"signing"`
	Verification VerificationConfig `mapstructure:"verification"`
}

// Load reads configs/config.{env}.yaml. ENV variables override yaml values
// (nested keys use "_" as the path separator, e.g. DATABASE_DSN).
func Load(env string) (*Config, error) {
	if env == "" {
		env = "test"
	}

	v := viper.New()
	v.SetConfigName(fmt.Sprintf("config.%s", env))
	v.SetConfigType("yaml")
	v.AddConfigPath("configs")
	v.AddConfigPath(".")

	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("config: read %s: %w", env, err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}

	// ENV PKI_ALLOWED_S3_BUCKETS=a,b,c приходит одной строкой —
	// после Unmarshal будет [{"a,b,c"}]. Разворачиваем в нормальный slice.
	if len(cfg.Verification.AllowedBuckets) == 1 &&
		strings.Contains(cfg.Verification.AllowedBuckets[0], ",") {
		parts := strings.Split(cfg.Verification.AllowedBuckets[0], ",")
		cfg.Verification.AllowedBuckets = cfg.Verification.AllowedBuckets[:0]
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				cfg.Verification.AllowedBuckets = append(cfg.Verification.AllowedBuckets, t)
			}
		}
	}

	return &cfg, nil
}
