package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Проверяет значения по умолчанию, переменные окружения и приоритет флагов.
func TestCommandConfiguration(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		args []string
		want runtimeConfig
	}{
		{
			name: "defaults",
			args: []string{"-d", "postgres://required"},
			want: runtimeConfig{
				runAddress:  "localhost:8080",
				databaseURI: "postgres://required",
			},
		},
		{
			name: "environment",
			env: map[string]string{
				"RUN_ADDRESS":            "env:8081",
				"DATABASE_URI":           "postgres://env",
				"ACCRUAL_SYSTEM_ADDRESS": "http://env-accrual",
			},
			want: runtimeConfig{
				runAddress:           "env:8081",
				databaseURI:          "postgres://env",
				accrualSystemAddress: "http://env-accrual",
			},
		},
		{
			name: "flags override environment",
			env: map[string]string{
				"RUN_ADDRESS":            "env:8081",
				"DATABASE_URI":           "postgres://env",
				"ACCRUAL_SYSTEM_ADDRESS": "http://env-accrual",
			},
			args: []string{
				"-a", "flag:8082",
				"-d", "postgres://flag",
				"-r", "http://flag-accrual",
			},
			want: runtimeConfig{
				runAddress:           "flag:8082",
				databaseURI:          "postgres://flag",
				accrualSystemAddress: "http://flag-accrual",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range []string{"RUN_ADDRESS", "DATABASE_URI", "ACCRUAL_SYSTEM_ADDRESS"} {
				unsetEnv(t, key)
			}
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			var got runtimeConfig
			called := false
			command := newCommand(func(_ context.Context, cfg runtimeConfig) error {
				called = true
				got = cfg
				return nil
			})

			err := command.Run(context.Background(), append([]string{"gophermart"}, tt.args...))

			require.NoError(t, err)
			assert.True(t, called)
			assert.Equal(t, tt.want, got)
		})
	}
}

// Проверяет остановку CLI до запуска приложения без обязательного адреса базы.
func TestCommandRequiresDatabaseURI(t *testing.T) {
	for _, key := range []string{"RUN_ADDRESS", "DATABASE_URI", "ACCRUAL_SYSTEM_ADDRESS"} {
		unsetEnv(t, key)
	}

	called := false
	command := newCommand(func(context.Context, runtimeConfig) error {
		called = true
		return nil
	})

	err := command.Run(context.Background(), []string{"gophermart"})

	require.Error(t, err)
	assert.False(t, called)
}

// Проверяет защитные таймауты HTTP-сервера.
func TestHTTPServerTimeouts(t *testing.T) {
	server := newHTTPServer(http.NotFoundHandler())

	assert.Equal(t, 5*time.Second, server.ReadHeaderTimeout)
	assert.Equal(t, 10*time.Second, server.ReadTimeout)
	assert.Equal(t, 10*time.Second, server.WriteTimeout)
	assert.Equal(t, 60*time.Second, server.IdleTimeout)
}

// Проверяет корректное завершение сервера после отмены контекста.
func TestServeShutsDownAfterCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	server := newHTTPServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	errCh := make(chan error, 1)
	go func() {
		errCh <- serve(ctx, server, listener, time.Second)
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://" + listener.Addr().String())
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusNoContent, response.StatusCode)

	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after context cancellation")
	}
}

// Проверяет возврат ошибки, если listener не может принимать соединения.
func TestServeReturnsListenerError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	require.NoError(t, listener.Close())

	err = serve(context.Background(), newHTTPServer(http.NotFoundHandler()), listener, time.Second)

	require.Error(t, err)
	assert.ErrorContains(t, err, "serve HTTP")
}

// Проверяет валидацию PostgreSQL до попытки открыть TCP-listener.
func TestRunServerValidatesDatabaseBeforeListener(t *testing.T) {
	const password = "top-secret-password"
	err := runServer(context.Background(), runtimeConfig{
		runAddress:  "127.0.0.1:-1",
		databaseURI: "postgres://gophermart:" + password + "@localhost:not-a-port/gophermart",
	}, zap.NewNop())

	require.Error(t, err)
	assert.ErrorContains(t, err, "open database")
	assert.NotContains(t, err.Error(), password)
}

// Проверяет использование стабильного секрета из окружения без преобразований.
func TestResolveSigningKeyUsesConfiguredSecret(t *testing.T) {
	key, err := resolveSigningKey("stable-development-secret")

	require.NoError(t, err)
	assert.Equal(t, []byte("stable-development-secret"), key)
}

// Проверяет генерацию нового 256-битного ключа при отсутствии конфигурации.
func TestResolveSigningKeyGeneratesFallback(t *testing.T) {
	key, err := resolveSigningKey("")

	require.NoError(t, err)
	assert.Len(t, key, 32)
}

// Проверяет ненулевой код завершения при неверном CLI-вызове.
func TestRealMainReturnsFailureForInvalidCLI(t *testing.T) {
	code := realMain([]string{"gophermart", "--unknown"}, func() (*zap.Logger, error) {
		return zap.NewNop(), nil
	})

	assert.Equal(t, 1, code)
}

// Проверяет ненулевой код завершения, если логгер не создан.
func TestRealMainReturnsFailureWhenLoggerCreationFails(t *testing.T) {
	code := realMain([]string{"gophermart"}, func() (*zap.Logger, error) {
		return nil, errors.New("logger unavailable")
	})

	assert.Equal(t, 1, code)
}

// Временно удаляет переменную окружения и восстанавливает её после теста.
func unsetEnv(t *testing.T, key string) {
	t.Helper()

	value, exists := os.LookupEnv(key)
	require.NoError(t, os.Unsetenv(key))
	t.Cleanup(func() {
		if exists {
			require.NoError(t, os.Setenv(key, value))
			return
		}
		require.NoError(t, os.Unsetenv(key))
	})
}
