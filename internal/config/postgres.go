package config

import "time"

type PostgresConfig struct {
	PostgresName           string        `env:"POSTGRES_NAME" envDefault:"postgres"`
	PostgresPassword       string        `env:"POSTGRES_PASSWORD" envDefault:"postgres"`
	PostgresHost           string        `env:"POSTGRES_HOST" envDefault:"localhost"`
	PostgresPort           string        `env:"POSTGRES_PORT" envDefault:"5432"`
	PostgresUsername       string        `env:"POSTGRES_USERNAME" envDefault:"postgres"`
	PostgresConnectTimeout time.Duration `env:"POSTGRES_CONNECT_TIMEOUT" envDefault:"10s"`
}
