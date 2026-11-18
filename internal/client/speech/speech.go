// Package speech описывает клиент распознавания речи.
//
// Пакет содержит только интерфейс и типы запроса-ответа: бизнес-логика зависит
// от этой абстракции, а не от конкретного поставщика API. Реализации лежат рядом
// (mock.go, yandex.go) и выбираются по конфигурации.
package speech

import "context"

// Client — клиент сервиса распознавания речи.
type Client interface {
	// Transcribe возвращает текст встречи. Для асинхронных внешних API метод
	// сам ждёт готовности результата, но обязан уважать отмену контекста.
	Transcribe(ctx context.Context, req Request) (Result, error)

	// Provider возвращает имя провайдера: оно попадает в лог и в базу.
	Provider() string
}

// Request — запрос на распознавание.
type Request struct {
	// Path — путь к локальному файлу встречи.
	Path string
	// FileName — имя файла без пути (для мессенджеров может быть сгенерировано).
	FileName string
	// Language — подсказка языка, пустое значение означает "определить самому".
	Language string
}

// Result — результат распознавания в едином для всех провайдеров формате.
type Result struct {
	// Text — распознанный текст.
	Text string
	// Language — язык, который определил провайдер.
	Language string
	// Provider — имя провайдера, полученное результат.
	Provider string
}
