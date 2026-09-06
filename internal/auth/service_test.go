package auth

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

type storedUser struct {
	id           int64
	passwordHash string
}

type memoryUserStore struct {
	nextID int64
	users  map[string]storedUser
}

// Создаёт пользователя в памяти с теми же результатами, что и PostgreSQL-операция.
func (s *memoryUserStore) CreateUser(_ context.Context, login, passwordHash string) (int64, bool, error) {
	if _, exists := s.users[login]; exists {
		return 0, false, nil
	}

	s.nextID++
	s.users[login] = storedUser{id: s.nextID, passwordHash: passwordHash}
	return s.nextID, true, nil
}

// Возвращает сохранённые учётные данные или признак отсутствующего логина.
func (s *memoryUserStore) UserCredentials(_ context.Context, login string) (int64, string, bool, error) {
	user, exists := s.users[login]
	return user.id, user.passwordHash, exists, nil
}

// Проверяет хеширование пароля, создание сессии и отказ для занятого логина.
func TestRegisterCreatesUserAndSession(t *testing.T) {
	users := &memoryUserStore{users: make(map[string]storedUser)}
	service, err := NewService(users, []byte("01234567890123456789012345678901"))
	require.NoError(t, err)

	now := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	session, err := service.Register(context.Background(), "alice", "secret")

	require.NoError(t, err)
	stored := users.users["alice"]
	assert.NotEqual(t, "secret", stored.passwordHash)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(stored.passwordHash), []byte("secret")))
	assert.Equal(t, now.Add(24*time.Hour), session.ExpiresAt)
	userID, err := service.Authenticate(session.Token)
	require.NoError(t, err)
	assert.Equal(t, stored.id, userID)

	_, err = service.Register(context.Background(), "alice", "another-password")
	require.ErrorIs(t, err, ErrLoginTaken)
}

// Проверяет отказ регистрации и входа при отсутствующем логине или пароле.
func TestAuthenticationRejectsEmptyCredentials(t *testing.T) {
	service := newTestService(t)

	operations := []struct {
		name string
		call func(context.Context, string, string) (Session, error)
	}{
		{name: "register", call: service.Register},
		{name: "login", call: service.Login},
	}
	credentials := []struct {
		name     string
		login    string
		password string
	}{
		{name: "empty login", password: "secret"},
		{name: "empty password", login: "alice"},
	}

	for _, operation := range operations {
		for _, credential := range credentials {
			t.Run(operation.name+"/"+credential.name, func(t *testing.T) {
				_, err := operation.call(context.Background(), credential.login, credential.password)
				require.ErrorIs(t, err, ErrInvalidInput)
			})
		}
	}
}

// Проверяет одинаковую ошибку для неизвестного логина и неверного пароля.
func TestLoginDoesNotRevealWhichCredentialIsWrong(t *testing.T) {
	service := newTestService(t)
	_, err := service.Register(context.Background(), "alice", "secret")
	require.NoError(t, err)

	session, err := service.Login(context.Background(), "alice", "secret")
	require.NoError(t, err)
	userID, err := service.Authenticate(session.Token)
	require.NoError(t, err)
	assert.Equal(t, int64(1), userID)

	for _, credentials := range []struct {
		login    string
		password string
	}{
		{login: "alice", password: "wrong"},
		{login: "unknown", password: "secret"},
	} {
		_, err := service.Login(context.Background(), credentials.login, credentials.password)
		require.ErrorIs(t, err, ErrInvalidCredentials)
	}
}

// Создаёт сервис с изолированным хранилищем и фиксированным тестовым ключом.
func newTestService(t *testing.T) *Service {
	t.Helper()

	service, err := NewService(
		&memoryUserStore{users: make(map[string]storedUser)},
		[]byte("01234567890123456789012345678901"),
	)
	require.NoError(t, err)
	return service
}
