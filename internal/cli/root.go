package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

// availableCommands — список команд для подсказки в сообщении об ошибке.
const availableCommands = "start, load, serve, status, list, get, summary, find, chat, retry, delete, export, stats; справка: --help"

// rootCommand собирает дерево команд.
//
// SilenceUsage и SilenceErrors включены намеренно: cobra не должна печатать
// справку при ошибке выполнения и не должна дублировать сообщение об ошибке —
// это делает main, один раз и в stderr.
func (a *App) rootCommand() *cobra.Command {
	// Режим фиксируется по исходным аргументам: значения флагов cobra пишет в
	// поля App при разборе, и проверять их после разбора ненадёжно.
	shell := a.shell

	root := &cobra.Command{
		Use:   "meeting-assistant",
		Short: "Умный помощник для конспектирования встреч",
		Long: `Загружает аудио- или тестовый файл встречи, получает транскрипцию и краткую
выжимку, сохраняет их в PostgreSQL, ищет по сохранённым материалам и отвечает
на вопросы по ним.

Пользователь задаётся флагом --user: при первом обращении он создаётся автоматически.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		// Запуск без команды (например, `ma.exe --user alice`) — ошибка, а не
		// справка: справка в узкой консоли обрезается и не говорит, что делать.
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return errors.New("неизвестная команда: " + args[0])
			}
			// Без команды, но с --user и --shell: сразу в интерактивный режим.
			if shell {
				if _, err := a.currentUser(cmd.Context()); err != nil {
					return err
				}
				a.userID = a.userFlag
				return nil
			}
			return errors.New("не указана команда: " + availableCommands)
		},
	}

	root.PersistentFlags().StringVar(&a.userFlag, "user", "",
		"идентификатор пользователя; обязателен для всех команд, кроме start")
	root.PersistentFlags().BoolVar(&a.jsonOutput, "json", false,
		"выводить результат в JSON для машинной обработки")
	root.PersistentFlags().BoolVar(&a.shell, "shell", false,
		"после команды открыть интерактивный режим (или сразу, если команды нет)")

	root.AddCommand(
		a.startCommand(),
		a.serveCommand(),
		a.loadCommand(),
		a.statusCommand(),
		a.listCommand(),
		a.getCommand(),
		a.summaryCommand(),
		a.findCommand(),
		a.chatCommand(),
		a.retryCommand(),
		a.deleteCommand(),
		a.exportCommand(),
		a.statsCommand(),
	)

	return root
}

// printer собирает вывод с учётом флага --json.
func (a *App) printer() printer {
	return printer{out: a.stdout, json: a.jsonOutput}
}
