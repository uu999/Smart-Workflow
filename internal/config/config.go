package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Config 是 Smart-Workflow 的全局配置。
// 来源优先级：环境变量 > 配置文件 > 默认值。
// 环境变量前缀 SWF_，嵌套用下划线，如 SWF_MYSQL_DSN、SWF_SIDECAR_BASEURL。
type Config struct {
	Env     string        `mapstructure:"env"`
	Server  ServerConfig  `mapstructure:"server"`
	MySQL   MySQLConfig   `mapstructure:"mysql"`
	Redis   RedisConfig   `mapstructure:"redis"`
	Sidecar SidecarConfig `mapstructure:"sidecar"`
	Log     LogConfig     `mapstructure:"log"`
}

type ServerConfig struct {
	Addr string `mapstructure:"addr"`
}

type MySQLConfig struct {
	DSN string `mapstructure:"dsn"`
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type SidecarConfig struct {
	BaseURL string `mapstructure:"baseurl"`
}

type LogConfig struct {
	Level string `mapstructure:"level"`
}

// Load 读取配置。configPath 为空时只用默认值 + 环境变量。
func Load(configPath string) (*Config, error) {
	v := viper.New()

	v.SetDefault("env", "dev")
	v.SetDefault("server.addr", ":8080")
	v.SetDefault("mysql.dsn", "swf:swfpass@tcp(127.0.0.1:3308)/smart_workflow?parseTime=true&charset=utf8mb4&loc=Local")
	v.SetDefault("redis.addr", "127.0.0.1:6381")
	v.SetDefault("redis.password", "")
	v.SetDefault("redis.db", 0)
	v.SetDefault("sidecar.baseurl", "http://127.0.0.1:8090")
	v.SetDefault("log.level", "info")

	v.SetEnvPrefix("SWF")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if configPath != "" {
		v.SetConfigFile(configPath)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("read config %s: %w", configPath, err)
		}
	}

	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	return &c, nil
}
