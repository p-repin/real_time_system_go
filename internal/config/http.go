package config

import "time"

// HTTPConfig — конфигурация HTTP-сервера.
//
// НАСТРОЙКИ:
// - Port: порт для прослушивания
// - ReadTimeout: таймаут на чтение запроса (защита от slowloris)
// - WriteTimeout: таймаут на запись ответа
// - IdleTimeout: таймаут keep-alive соединения
// - ShutdownTimeout: время на graceful shutdown
type HTTPConfig struct {
	// Port — порт HTTP-сервера (например, 8080)
	Port string `env:"HTTP_PORT" envDefault:"8080"`

	// ReadTimeout — максимальное время на чтение всего запроса.
	// Включает чтение headers и body.
	// Защищает от slowloris-атак (медленная отправка данных).
	ReadTimeout time.Duration `env:"HTTP_READ_TIMEOUT" envDefault:"15s"`

	// WriteTimeout — максимальное время на запись ответа.
	// Начинается после чтения headers запроса.
	WriteTimeout time.Duration `env:"HTTP_WRITE_TIMEOUT" envDefault:"15s"`

	// IdleTimeout — максимальное время ожидания следующего запроса
	// на keep-alive соединении. После этого соединение закрывается.
	IdleTimeout time.Duration `env:"HTTP_IDLE_TIMEOUT" envDefault:"60s"`

	// ShutdownTimeout — время на graceful shutdown.
	// Сервер ждёт завершения активных запросов, потом закрывается.
	ShutdownTimeout time.Duration `env:"HTTP_SHUTDOWN_TIMEOUT" envDefault:"30s"`
}

// Addr возвращает адрес для прослушивания (":8080").
func (c HTTPConfig) Addr() string {
	return ":" + c.Port
}
