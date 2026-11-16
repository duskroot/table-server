package app

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"table/internal/actor"
	"table/internal/config"
	"table/internal/httpx"
	"table/internal/play"
	"table/internal/record"
	"table/internal/room"
	"table/internal/rule"
	"table/internal/session"
	"table/internal/store"
	"table/internal/wechat"
)

func Run(ctx context.Context, cfg config.Config) error {
	if cfg.MySQLDSN == "" {
		return fmt.Errorf("database config is empty; set CONFIG_TOKEN in .env or MYSQL_DSN")
	}
	db, err := sql.Open("mysql", cfg.MySQLDSN)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(30)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(30 * time.Minute)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = db.PingContext(pingCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("mysql: %w", err)
	}
	if err := store.Migrate(ctx, db); err != nil {
		return err
	}
	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword, DB: cfg.RedisDB})
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis: %w", err)
	}
	repo := store.NewMySQL(db)
	live := session.NewRedis(rdb)
	hub := actor.New()
	sessions := session.New(repo, live, wechat.Client{AppID: cfg.WeChatAppID, Secret: cfg.WeChatSecret}, cfg.SessionTTL)
	rooms := room.New(repo, hub, live)
	plays := play.New(repo, hub, live)
	records := record.New(repo)
	go plays.RunTicker(ctx)
	slog.Info("table server", "addr", cfg.Addr, "rules", rule.Codes())
	engine := httpx.New(httpx.Deps{
		Session: sessions, Room: rooms, Play: plays, Record: records, Dev: cfg.Dev, Addr: cfg.Addr,
	})
	return engine.Run()
}
