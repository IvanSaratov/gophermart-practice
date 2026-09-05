package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ivansaratov/gophermart-practice/internal/api"
	"github.com/ivansaratov/gophermart-practice/internal/observability"
	"github.com/ivansaratov/gophermart-practice/internal/store"
	"github.com/urfave/cli/v3"
	"go.uber.org/zap"
)

// Пока хардкод.
const (
	gracefulShutdownTimeout = 10 * time.Second
	databaseStartupTimeout  = 10 * time.Second
)

type runtimeConfig struct {
	runAddress           string
	databaseURI          string
	accrualSystemAddress string
}

type actionFunc func(context.Context, runtimeConfig) error

type loggerFactory func() (*zap.Logger, error)

// Запускает приложение и возвращает ОС код его завершения.
func main() {
	os.Exit(realMain(os.Args, observability.NewLogger))
}

// Управляет логгером, сигналами завершения и кодом возврата процесса.
func realMain(args []string, makeLogger loggerFactory) int {
	logger, err := makeLogger()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "create logger: %v\n", err)
		return 1
	}
	// На терминальных потоках Sync может вернуть EINVAL, который не влияет на доставку логов.
	defer func() { _ = logger.Sync() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	command := newCommand(func(ctx context.Context, cfg runtimeConfig) error {
		return runServer(ctx, cfg, logger)
	})
	if err := command.Run(ctx, args); err != nil {
		logger.Error("Application stopped with error", zap.Error(err))
		return 1
	}

	return 0
}

// Создаёт CLI-команду и передаёт в приложение параметры с учётом флагов и окружения.
func newCommand(action actionFunc) *cli.Command {
	return &cli.Command{
		Name:  "gophermart",
		Usage: "run Gophermart service",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "address",
				Aliases: []string{"a"},
				Value:   "localhost:8080",
				Sources: cli.EnvVars("RUN_ADDRESS"),
			},
			&cli.StringFlag{
				Name:     "database-uri",
				Aliases:  []string{"d"},
				Usage:    "PostgreSQL connection URI",
				Required: true,
				Sources:  cli.EnvVars("DATABASE_URI"),
				Config:   cli.StringConfig{TrimSpace: true},
			},
			&cli.StringFlag{
				Name:    "accrual-system-address",
				Aliases: []string{"r"},
				Sources: cli.EnvVars("ACCRUAL_SYSTEM_ADDRESS"),
			},
		},
		Action: func(ctx context.Context, command *cli.Command) error {
			return action(ctx, runtimeConfig{
				runAddress:           command.String("address"),
				databaseURI:          command.String("database-uri"),
				accrualSystemAddress: command.String("accrual-system-address"),
			})
		},
	}
}

// Подключает PostgreSQL, собирает операционный API и запускает HTTP-сервер.
func runServer(ctx context.Context, cfg runtimeConfig, logger *zap.Logger) error {
	startupCtx, cancelStartup := context.WithTimeout(ctx, databaseStartupTimeout)
	database, err := store.Open(startupCtx, cfg.databaseURI)
	cancelStartup()
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	logger.Info("Database connection established")

	metrics := observability.NewMetrics()
	handler := api.NewRouter(logger, metrics, database.Ping)
	server := newHTTPServer(handler)

	listener, err := net.Listen("tcp", cfg.runAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.runAddress, err)
	}
	// Ошибка повторного закрытия не влияет на завершение, но ресурс должен освобождаться на всех путях возврата.
	defer func() { _ = listener.Close() }()

	logger.Info("HTTP server started", zap.String("address", listener.Addr().String()))
	if err := serve(ctx, server, listener, gracefulShutdownTimeout); err != nil {
		return err
	}
	logger.Info("HTTP server stopped")

	return nil
}

// Создаёт HTTP-сервер с ограничениями на чтение, запись и простой соединения.
func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler: handler,
		// Ограничения защищают сервер от медленных или зависших клиентских соединений.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

// Обслуживает HTTP-запросы до отмены контекста и завершает активные соединения в заданный срок.
func serve(ctx context.Context, server *http.Server, listener net.Listener, shutdownTimeout time.Duration) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return fmt.Errorf("shutdown HTTP server: %w", err)
		}

		err := <-errCh
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP after shutdown: %w", err)
		}
		return nil
	}
}
