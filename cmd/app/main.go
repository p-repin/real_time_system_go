// Package main — точка входа приложения.
//
// ПОЧЕМУ пакет называется main, а не app:
// В Go исполняемый файл (binary) создаётся только из пакета main с функцией main().
// Если пакет называется "app", компилятор не создаст исполняемый файл —
// go build просто проигнорирует его, а go run выдаст ошибку.
// Это требование спецификации языка, а не конвенция.
//
// ПОЧЕМУ main() минимален:
// Main — это "клей", который создаёт зависимости и запускает приложение.
// Вся логика инициализации и shutdown — в internal/server.
// Это позволяет тестировать Server отдельно, без os.Exit и сигналов.

// ┌──────────────────────────────────────────────────────────────────────────┐
// │ SWAGGER GENERAL API INFO                                                   │
// └──────────────────────────────────────────────────────────────────────────┘
//
// Эти комментарии читает swag init и генерирует docs/swagger.json.
// Формат: @название значение
//
// После изменений запускай: swag init -g cmd/app/main.go

// SWAGGER UI:
// После запуска сервера открой http://localhost:8080/swagger/index.html

// @title Real Time System API
// @version 1.0
// @description E-commerce платформа с real-time возможностями.
// @description
// @description ## Особенности
// @description - RESTful API с версионированием (/api/v1)
// @description - Soft delete для всех entities
// @description - Partial update через PATCH
// @description - Structured error responses

// @contact.name API Support

// @host localhost:8080
// @BasePath /

// @schemes http https

package main

import (
	"context"
	"log"

	"real_time_system/internal/logger"
	"real_time_system/internal/server"
)

func main() {
	logger.Init()

	srv, err := server.New(context.Background())
	if err != nil {
		log.Fatalf("init server: %v", err)
	}
	defer srv.Close()

	if err := srv.Run(); err != nil {
		log.Fatalf("run server: %v", err)
	}
}
