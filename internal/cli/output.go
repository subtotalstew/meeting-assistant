package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// Форматы времени в выводе.
const (
	timeLayout = "2006-01-02 15:04:05"
	emptyValue = "—"
)

// printer выводит результат в человекочитаемом или машинном виде.
//
// Два режима нужны и на защите, и в тестах: таблицу удобно читать, JSON — парсить
// и сравнивать, не завися от ширины колонок.
type printer struct {
	out  io.Writer
	json bool
}

// writeJSON печатает значение в формате JSON с отступами.
func (p printer) writeJSON(value any) error {
	encoder := json.NewEncoder(p.out)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("вывод JSON: %w", err)
	}
	return nil
}

// writeTable печатает таблицу с выравниванием по ширине колонок.
func (p printer) writeTable(headers []string, rows [][]string) error {
	widths := make([]int, len(headers))
	for i, header := range headers {
		widths[i] = utf8.RuneCountInString(header)
	}

	for _, row := range rows {
		for i, cell := range row {
			if i >= len(widths) {
				break
			}
			if width := utf8.RuneCountInString(cell); width > widths[i] {
				widths[i] = width
			}
		}
	}

	var builder strings.Builder

	for i, header := range headers {
		builder.WriteString(pad(header, widths[i]))
		if i < len(headers)-1 {
			builder.WriteString("  ")
		}
	}
	builder.WriteString("\n")

	for i := range headers {
		builder.WriteString(strings.Repeat("-", widths[i]))
		if i < len(headers)-1 {
			builder.WriteString("  ")
		}
	}
	builder.WriteString("\n")

	for _, row := range rows {
		for i := range headers {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			builder.WriteString(pad(cell, widths[i]))
			if i < len(headers)-1 {
				builder.WriteString("  ")
			}
		}
		builder.WriteString("\n")
	}

	if _, err := io.WriteString(p.out, builder.String()); err != nil {
		return fmt.Errorf("вывод таблицы: %w", err)
	}
	return nil
}

// pad дополняет строку пробелами до нужной ширины.
func pad(value string, width int) string {
	missing := width - utf8.RuneCountInString(value)
	if missing <= 0 {
		return value
	}
	return value + strings.Repeat(" ", missing)
}

// formatTime приводит время к читаемому виду в локальной зоне.
func formatTime(value time.Time) string {
	if value.IsZero() {
		return emptyValue
	}
	return value.Local().Format(timeLayout)
}

// orEmpty заменяет пустое значение прочерком, чтобы таблица не «прыгала».
func orEmpty(value string) string {
	if strings.TrimSpace(value) == "" {
		return emptyValue
	}
	return value
}

// truncateText обрезает длинный текст для таблицы.
func truncateText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}
