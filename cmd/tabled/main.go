// tabled serves the drinking-table HTTP API.
//
//	HTTP_ADDR=:8080
//	CONFIG_TOKEN=cfg_...     pulls table-server config from Eosconfig
//	CONFIG_ENV=prod
//	MYSQL_DSN=               used only when CONFIG_TOKEN is empty
//	REDIS_ADDR=127.0.0.1:6379
//	REDIS_PASSWORD=
//	WECHAT_APPID=      empty uses dev login, openid is dev_ plus Code
//	WECHAT_SECRET=
//	DEV=1              include system-error detail
package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"table/internal/app"
	"table/internal/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	cfg, err := config.Load(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if cfg.ConfigVersion > 0 {
		log.Printf("Eosconfig version %d", cfg.ConfigVersion)
	}
	if err := app.Run(ctx, cfg); err != nil {
		log.Fatal(err)
	}
}
