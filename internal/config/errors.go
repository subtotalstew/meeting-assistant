package config

import (
	"errors"
	"fmt"
	"net/url"
)

// ErrInvalidConfig — общая причина ошибок конфигурации. Проверяется через errors.Is.
var ErrInvalidConfig = errors.New("некорректная конфигурация")

// redactDSN оставляет от строки подключения только схему, хост и имя базы.
//
// Пароль не должен попадать в лог даже в сообщении об ошибке: логи читают люди
// и системы, а строка подключения — самый вероятный источник утечки секрета.
func redactDSN(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "<неразбираемый DSN>"
	}

	if parsed.User != nil {
		parsed.User = url.User(parsed.User.Username())
	}
	parsed.RawQuery = ""

	return parsed.Redacted()
}

// fieldError собирает ошибку проверки конкретного поля.
func fieldError(field string, value any, reason string) error {
	return fmt.Errorf("%w: %s=%v %s", ErrInvalidConfig, field, value, reason)
}
