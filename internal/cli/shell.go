package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

// Оболочка — интерактивный режим CLI: один процесс, один пул, много команд.
//
// Зачем: обычный вызов `ma.exe load file.ogg --user alice` каждый раз поднимает
// конфигурацию, соединение с базой и пул воркеров, а затем завершается. Для
// сценария из нескольких шагов это медленно и требует повторять --user и -m.
// В оболочке состояние (пользователь, последняя встреча) живёт в памяти, а
// соединение с базой и пул поднимаются один раз.
//
// Оболочка не содержит бизнес-логики: каждая строка разбирается и передаётся
// в то же дерево команд cobra, что и обычный запуск. Это ещё один адаптер.

// runShell запускает интерактивный режим для уже зарегистрированного пользователя.
//
// Пул воркеров стартует сразу: задача, загруженная в оболочке, начинает
// обрабатываться без отдельного serve. Отмена текущей команды (Ctrl+C) не
// завершает оболочку: команда получает отменённый контекст, цикл продолжается.
func (a *App) runShell(ctx context.Context, name string) error {
	// В оболочке пул уже должен работать: задача, загруженная в сессии,
	// начинает обрабатываться без отдельного serve.
	if a.worker != nil {
		a.worker.Start(ctx)
	}

	return a.runShellFrom(ctx, name, os.Stdin)
}

// runShellFrom — то же самое, но с явным источником ввода: так оболочку
// можно прогнать в тесте через strings.Reader.
func (a *App) runShellFrom(ctx context.Context, name string, in io.Reader) error {
	// Ctrl+C во время команды отменяет только её: процесс продолжает работать.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	lines, scanErr := readLines(ctx, in)

	a.printf("интерактивный режим, пользователь %s\n", name)
	a.printf("команды без --user; help — справка, exit — выход\n")

	// cancel отменяет текущую команду; после отмены создаётся новый контекст.
	// Отмена вынесена в отдельную переменную, чтобы vet видел: все пути её вызывают.
	var cancel context.CancelFunc
	defer func() { cancel() }()

	cmdCtx, cancel := context.WithCancel(ctx)

	for {
		a.printf("%s> ", name)

		var line string
		select {
		case <-ctx.Done():
			cancel()
			return nil
		case <-signals:
			continue
		case err := <-scanErr:
			if err == nil || errors.Is(err, io.EOF) {
				a.println()
				cancel()
				return nil
			}
			cancel()
			return fmt.Errorf("чтение ввода: %w", err)
		case line = <-lines:
		}

		args, err := splitArgs(strings.TrimSpace(line))
		if err != nil {
			a.printf("ошибка: %v\n", err)
			continue
		}
		if len(args) == 0 {
			continue
		}

		switch args[0] {
		case "exit", "quit":
			cancel()
			return nil
		case "help", "?":
			printShellHelp(a)
			continue
		}

		// Команда идёт в отдельной горутине: пока она работает, оболочка
		// слушает Ctrl+C и может отменить только её, а не весь процесс.
		done := make(chan error, 1)
		go func(args []string) {
			done <- a.executeLine(cmdCtx, args)
		}(args)

		select {
		case err := <-done:
			if err != nil {
				a.printf("ошибка: %v\n", err)
			}
		case <-signals:
			cancel()
			<-done
			cmdCtx, cancel = context.WithCancel(ctx)
			a.printf("\nкоманда прервана\n")
		}
	}
}

// executeLine выполняет одну строку оболочки как обычную команду CLI.
//
// Дерево команд собирается заново: cobra хранит значения флагов между запусками,
// и переиспользование одного дерева меняло бы поведение следующей команды.
//
// Флаги строки, которых нет в аргументах (--user, -m), переносятся в поля App до
// сборки дерева: команды читают именно поля, а не сами аргументы.
func (a *App) executeLine(ctx context.Context, args []string) error {
	// В оболочке пользователь известен из сессии, поэтому в строке его нет:
	// выставляем флаг до разбора аргументов.
	if a.userID != "" {
		a.userFlag = a.userID
	}

	if m := a.flagValue(args, []string{"-m", "--meeting"}); m != "" {
		a.chatMeeting = m
	} else if a.lastMeeting != "" && a.wantsMeeting(args) {
		// chat ждёт встречу во флаге, остальные команды — аргументом.
		// Флаг, а не аргумент: cobra разбирает аргументы после того, как
		// значения полей уже прочитаны, и добавленный аргумент до chat не дойдёт.
		if wantsPositionalMeetingID(args) && !hasPositionalMeetingID(args) {
			args = append(args, a.lastMeeting)
		} else if args[0] == "chat" {
			args = append(args, "-m", a.lastMeeting)
		}
	}

	cmd := a.rootCommand()
	cmd.SetArgs(args)
	cmd.SetOut(a.stdout)
	cmd.SetErr(a.stderr)

	return cmd.ExecuteContext(ctx)
}

// wantsPositionalMeetingID сообщает, ждёт ли команда идентификатор встречи
// аргументом. У chat он передаётся флагом -m, поэтому сюда не входит.
func wantsPositionalMeetingID(args []string) bool {
	if len(args) == 0 {
		return false
	}

	switch args[0] {
	case "status", "get", "summary", "retry", "delete", "export":
		return true
	}
	return false
}

// hasPositionalMeetingID сообщает, передан ли идентификатор встречи аргументом.
func hasPositionalMeetingID(args []string) bool {
	return len(args) >= 2 && wantsPositionalMeetingID(args)
}

// flagValue возвращает значение флага в формах `-m x`, `--meeting x`, `--meeting=x`.
func (a *App) flagValue(args []string, names []string) string {
	for i, arg := range args {
		for _, name := range names {
			if arg == name && i+1 < len(args) {
				return args[i+1]
			}
			if strings.HasPrefix(arg, name+"=") {
				return strings.TrimPrefix(arg, name+"=")
			}
		}
	}
	return ""
}

// wantsMeeting сообщает, может ли команда взять последнюю встречу из сессии.
//
// status/get/summary/retry/delete/export принимают её аргументом, chat — флагом.
// Для остальных (list, find, stats, load) id не нужен, и подставлять его нельзя.
func (a *App) wantsMeeting(args []string) bool {
	if len(args) == 0 {
		return false
	}

	if args[0] == "chat" {
		return true
	}

	if len(args) > 1 {
		// Идентификатор уже указан явно.
		return false
	}

	switch args[0] {
	case "status", "get", "summary", "retry", "delete", "export":
		return true
	}
	return false
}

// readLines читает ввод построчно в отдельной горутине, чтобы основной цикл мог
// одновременно ждать строку, сигнал и отмену.
func readLines(ctx context.Context, in io.Reader) (<-chan string, <-chan error) {
	lines := make(chan string)
	errs := make(chan error, 1)

	go func() {
		scanner := bufio.NewScanner(in)
		// Длинный вопрос в chat не должен обрезаться на 64 KiB по умолчанию.
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
		errs <- scanner.Err()
	}()

	return lines, errs
}

// printShellHelp печатает подсказку по командам оболочки.
func printShellHelp(a *App) {
	a.println("команды (--user уже задан, идентификатор встречи необязателен):")
	a.println("  load <path>        загрузить файл встречи")
	a.println("  status [<id>]      статус обработки")
	a.println("  list               список встреч")
	a.println("  get [<id>]         транскрипция")
	a.println("  summary [<id>]     краткая выжимка")
	a.println("  find <слово>       поиск по своим встречам")
	a.println(`  chat "вопрос"      вопрос по материалам встречи`)
	a.println("  retry [<id>]       повторить обработку")
	a.println("  delete [<id>]      удалить встречу")
	a.println("  export [<id>]      сохранить транскрипцию в файл")
	a.println("  stats              статистика")
	a.println("  exit               выход")
}

// splitArgs разбивает строку на аргументы с поддержкой кавычек:
// `chat "когда релиз"` даёт два аргумента, а не три.
//
// Собственный парсер вместо библиотеки: нужно ровно одно правило — двойные
// кавычки группируют слова, внутри них пробелы значимы.
func splitArgs(line string) ([]string, error) {
	var (
		args    []string
		current strings.Builder
		quoted  bool
		started bool
	)

	for _, r := range line {
		switch {
		case r == '"':
			quoted = !quoted
			started = true
		case (r == ' ' || r == '\t') && !quoted:
			if started {
				args = append(args, current.String())
				current.Reset()
				started = false
			}
		default:
			current.WriteRune(r)
			started = true
		}
	}

	if quoted {
		return nil, errors.New(`незакрытая кавычка: закройте вопрос символом "`)
	}
	if started {
		args = append(args, current.String())
	}

	return args, nil
}
