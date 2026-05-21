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

type Config struct {
	App       AppConfig       `mapstructure:"app"`
	Database  DatabaseConfig  `mapstructure:"database"`
	NCANode   NCANodeConfig   `mapstructure:"ncanode"`
	Storage   StorageConfig   `mapstructure:"storage"`
	Redis     RedisConfig     `mapstructure:"redis"`
	RabbitMQ  RabbitMQConfig  `mapstructure:"rabbitmq"`
	Log       LogConfig       `mapstructure:"log"`
	RateLimit RateLimitConfig `mapstructure:"rate_limit"`
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
	return &cfg, nil
}
