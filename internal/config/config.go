package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
)

type Config struct {
	Addr          string
	MySQLDSN      string
	RedisAddr     string
	RedisPassword string
	RedisDB       int
	WeChatAppID   string
	WeChatSecret  string
	SessionTTL    time.Duration
	Dev           bool
	ConfigVersion int
}

func Load(ctx context.Context) (Config, error) {
	loadDotEnv()
	cfg := fromEnv()
	if os.Getenv("CONFIG_TOKEN") == "" {
		return cfg, nil
	}
	content, version, err := pull(ctx, remoteSettings{
		BaseURL:     getenv("CONFIG_URL", "https://config.nginx.systems"),
		Namespace:   getenv("CONFIG_NAMESPACE", "dev"),
		Environment: getenv("CONFIG_ENV", "prod"),
		AppID:       getenv("CONFIG_APP_ID", "table-server"),
		File:        getenv("CONFIG_FILE", "application"),
		Token:       os.Getenv("CONFIG_TOKEN"),
	})
	if err != nil {
		return Config{}, err
	}
	if err := applyRemote(&cfg, content); err != nil {
		return Config{}, err
	}
	cfg.ConfigVersion = version
	return cfg, nil
}

func fromEnv() Config {
	ttl := 168 * time.Hour
	if v := os.Getenv("SESSION_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			ttl = d
		}
	}
	db, _ := strconv.Atoi(os.Getenv("REDIS_DB"))
	return Config{
		Addr:          getenv("HTTP_ADDR", ":8080"),
		MySQLDSN:      os.Getenv("MYSQL_DSN"),
		RedisAddr:     getenv("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPassword: os.Getenv("REDIS_PASSWORD"),
		RedisDB:       db,
		WeChatAppID:   os.Getenv("WECHAT_APPID"),
		WeChatSecret:  os.Getenv("WECHAT_SECRET"),
		SessionTTL:    ttl,
		Dev:           os.Getenv("DEV") == "1" || os.Getenv("DEV") == "true",
	}
}

func applyRemote(cfg *Config, content json.RawMessage) error {
	var values map[string]any
	if err := json.Unmarshal(content, &values); err != nil {
		return fmt.Errorf("config center content: %w", err)
	}
	dsn, err := mysqlDSN(values)
	if err != nil {
		return err
	}
	addr, err := requiredString(values, "redis.addr")
	if err != nil {
		return err
	}
	password, err := requiredString(values, "redis.password")
	if err != nil {
		return err
	}
	db, err := requiredInt(values, "redis.db")
	if err != nil {
		return err
	}
	cfg.MySQLDSN = dsn
	cfg.RedisAddr = addr
	cfg.RedisPassword = password
	cfg.RedisDB = db
	return nil
}

func mysqlDSN(values map[string]any) (string, error) {
	host, err := requiredString(values, "mysql.host")
	if err != nil {
		return "", err
	}
	port, err := requiredInt(values, "mysql.port")
	if err != nil {
		return "", err
	}
	user, err := requiredString(values, "mysql.user")
	if err != nil {
		return "", err
	}
	password, err := requiredString(values, "mysql.password")
	if err != nil {
		return "", err
	}
	database, err := requiredString(values, "mysql.database")
	if err != nil {
		return "", err
	}
	driver := mysql.NewConfig()
	driver.User = user
	driver.Passwd = password
	driver.Net = "tcp"
	driver.Addr = fmt.Sprintf("%s:%d", host, port)
	driver.DBName = database
	driver.ParseTime = true
	driver.Loc = time.UTC
	driver.Params = map[string]string{"charset": "utf8mb4"}
	return driver.FormatDSN(), nil
}

func requiredString(values map[string]any, key string) (string, error) {
	value, ok := values[key]
	if !ok {
		return "", fmt.Errorf("config center missing %s", key)
	}
	switch typed := value.(type) {
	case string:
		if typed == "" {
			return "", fmt.Errorf("config center missing %s", key)
		}
		return typed, nil
	default:
		return "", fmt.Errorf("config center %s must be a string", key)
	}
}

func requiredInt(values map[string]any, key string) (int, error) {
	value, ok := values[key]
	if !ok {
		return 0, fmt.Errorf("config center missing %s", key)
	}
	switch typed := value.(type) {
	case float64:
		return int(typed), nil
	case string:
		n, err := strconv.Atoi(typed)
		if err != nil {
			return 0, fmt.Errorf("config center %s must be a number", key)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("config center %s must be a number", key)
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
