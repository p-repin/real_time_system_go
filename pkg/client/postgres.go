package client

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"real_time_system/internal/config"
	"real_time_system/internal/logger"
)

type Postgres struct {
	*pgxpool.Pool
}

func NewPostgres(ctx context.Context, cfg *config.Config) (*Postgres, error) {
	l := logger.FromContext(ctx)

	// sslmode=disable для локальной разработки
	// В production используй sslmode=require или sslmode=verify-full
	dsn := fmt.Sprintf("postgresql://%s:%s@%s:%s/%s?sslmode=disable",
		cfg.PostgresUsername,
		cfg.PostgresPassword,
		cfg.PostgresHost,
		cfg.PostgresPort,
		cfg.PostgresName,
	)

	l.Infof("Connecting to postgresql(host: %s, port:%s)", cfg.PostgresHost, cfg.PostgresPort)

	ctx, cancel := context.WithTimeout(ctx, cfg.PostgresConnectTimeout)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return &Postgres{Pool: pool}, nil

}
