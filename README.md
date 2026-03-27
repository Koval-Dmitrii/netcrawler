# 🕸️ Network Crawler

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go)](https://golang.org)
[![Concurrency](https://img.shields.io/badge/concurrency-worker%20pool-success?style=flat)](https://go.dev/doc/effective_go#concurrency)
[![HTTP](https://img.shields.io/badge/http-server-blue?style=flat)](https://pkg.go.dev/net/http)

Многопоточный HTTP-сервер для обхода URL-адресов с поддержкой:
- кэширования ответов (`TTL = 1s`);
- нормализации URL;
- дедупликации параллельных запросов через `singleflight`;
- graceful shutdown при отмене контекста.

Проект реализован на паттернах `generator` + `worker pool` + `singleflight`и ориентирован на стабильную работу под нагрузкой.

## ✨ Возможности

- `POST /crawl` с батчем URL и настраиваемым количеством воркеров.
- Параллельный обход URL с сохранением **порядка результатов**, как во входном запросе.
- Для каждого URL возвращается либо `status_code`, либо `error`.
- Кэш на 1 секунду для повторных обращений.
- Защита от дублирующихся одновременных запросов к одному URL (`singleflight`).
- Корректное завершение сервера: `Shutdown` с ожиданием до 10 секунд.

## 📦 Установка

```bash
go get github.com/Koval-Dmitrii/netcrawler
```

## 🚀 Быстрый старт

```go
package main

import (
	"context"
	"log"

	"github.com/Koval-Dmitrii/netcrawler/internal/crawler"
)

func main() {
	ctx := context.Background()

	c := crawler.New()
	if err := c.ListenAndServe(ctx, ":8080"); err != nil {
		log.Fatal(err)
	}
}
```

Запрос:

```bash
curl --location 'http://127.0.0.1:8080/crawl' \
--header 'Content-Type: application/json' \
--data '{
  "urls": [
    "https://google.com",
    "https://dzen.ru"
  ],
  "workers": 2,
  "timeout_ms": 2000
}'
```

Ответ:

```json
[
  {
    "url": "https://google.com",
    "status_code": 200
  },
  {
    "url": "https://dzen.ru",
    "status_code": 200
  }
]
```

## 📚 API

```go
type Crawler interface {
	ListenAndServe(ctx context.Context, address string) error
}
```

```go
type CrawlRequest struct {
	URLs      []string `json:"urls"`
	Workers   int      `json:"workers"`    // количество воркеров
	TimeoutMS int      `json:"timeout_ms"` // таймаут на обработку всех URL
}
```

```go
type CrawlResponse struct {
	URL        string `json:"url"`
	StatusCode int    `json:"status_code,omitempty"`
	Error      string `json:"error,omitempty"`
}
```

## 🧪 Тестирование

Запуск всех тестов:

```bash
go test -v ./...
```

Запуск race-тестов:

```bash
go test -race ./internal/crawler
```

Проверка производительности:

```bash
go test -bench=. -benchmem ./internal/crawler
```

## 🏗️ Структура проекта

```text
netcrawler/
├── internal/
│   └── crawler/
│       ├── crawler.go
│       ├── generator.go
│       ├── workerpool.go
│       ├── singleflight.go
│       ├── model_test.go
│       ├── performance_test.go
│       ├── race_test.go
│       └── util_test.go
├── go.mod
└── README.md
```

## 👨‍💻 Автор

**Коваль Дмитрий**

- GitHub: [@Koval-Dmitrii](https://github.com/Koval-Dmitrii)
