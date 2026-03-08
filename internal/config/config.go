package config

import (
	"fmt"

	"github.com/caarlos0/env/v10"
	"github.com/joho/godotenv"
)

type Config struct {
	PostgresConfig
	HTTPConfig
	KafkaConfig
	ObservabilityConfig
}

// LoadConfig загружает конфигурацию из .env файла и переменных окружения.
//
// ПОРЯДОК ЗАГРУЗКИ:
// 1. godotenv загружает .env файл в os.Environ()
// 2. env.Parse() читает из os.Environ()
//
// Переменные окружения имеют приоритет над .env файлом.
// Это удобно для Docker/Kubernetes: .env для локальной разработки,
// environment variables для production.
func LoadConfig() (*Config, error) {
	// Загружаем .env файл (игнорируем ошибку, если файла нет)
	// В production .env может отсутствовать — переменные придут из окружения
	_ = godotenv.Load()

	var cfg Config

	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	return &cfg, nil
}
