package config

import (
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestMySQLDSNKeepsPassword(t *testing.T) {
	dsn, err := mysqlDSN(map[string]any{
		"mysql.host":     "110.40.178.18",
		"mysql.port":     float64(3306),
		"mysql.user":     "root",
		"mysql.password": "a&b",
		"mysql.database": "table_party",
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Passwd != "a&b" || parsed.Addr != "110.40.178.18:3306" || parsed.DBName != "table_party" || !parsed.ParseTime {
		t.Fatalf("parsed dsn user=%s addr=%s db=%s passwd=%s", parsed.User, parsed.Addr, parsed.DBName, parsed.Passwd)
	}
}

func TestApplyRemoteRedisDB(t *testing.T) {
	cfg := Config{}
	err := applyRemote(&cfg, []byte(`{"mysql.host":"10.0.0.1","mysql.port":3306,"mysql.user":"root","mysql.password":"secret","mysql.database":"table_party","redis.addr":"10.0.0.1:6379","redis.password":"secret","redis.db":7}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RedisAddr != "10.0.0.1:6379" || cfg.RedisDB != 7 || cfg.RedisPassword != "secret" {
		t.Fatalf("redis addr=%s db=%d", cfg.RedisAddr, cfg.RedisDB)
	}
	if cfg.MySQLDSN == "" {
		t.Fatal("empty dsn")
	}
}
