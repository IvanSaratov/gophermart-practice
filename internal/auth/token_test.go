package auth

import (
	"context"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Проверяет непредсказуемый 256-битный ключ для запуска без конфигурации.
func TestGenerateSigningKeyReturnsIndependentKeys(t *testing.T) {
	first, err := GenerateSigningKey()
	require.NoError(t, err)
	second, err := GenerateSigningKey()
	require.NoError(t, err)

	assert.Len(t, first, 32)
	assert.Len(t, second, 32)
	assert.NotEqual(t, first, second)
}

// Проверяет отклонение истёкшей и подписанной другим ключом сессии.
func TestAuthenticateRejectsUntrustedSession(t *testing.T) {
	users := &memoryUserStore{users: make(map[string]storedUser)}
	service, err := NewService(users, []byte("01234567890123456789012345678901"))
	require.NoError(t, err)

	now := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	session, err := service.Register(context.Background(), "alice", "secret")
	require.NoError(t, err)

	service.now = func() time.Time { return now.Add(24*time.Hour + time.Second) }
	_, err = service.Authenticate(session.Token)
	require.ErrorIs(t, err, ErrInvalidToken)

	otherService, err := NewService(users, []byte("different-key-012345678901234567"))
	require.NoError(t, err)
	otherService.now = func() time.Time { return now }
	_, err = otherService.Authenticate(session.Token)
	require.ErrorIs(t, err, ErrInvalidToken)
}

// Проверяет обязательный алгоритм, срок действия и положительный числовой subject.
func TestAuthenticateRequiresTrustedClaims(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	service, err := NewService(&memoryUserStore{users: make(map[string]storedUser)}, key)
	require.NoError(t, err)

	now := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	validExpiration := jwt.NewNumericDate(now.Add(time.Hour))
	issuedAt := jwt.NewNumericDate(now)
	tests := []struct {
		name   string
		method jwt.SigningMethod
		claims jwt.RegisteredClaims
	}{
		{
			name:   "unsupported algorithm",
			method: jwt.SigningMethodHS384,
			claims: jwt.RegisteredClaims{Subject: "1", IssuedAt: issuedAt, ExpiresAt: validExpiration},
		},
		{
			name:   "missing expiration",
			method: jwt.SigningMethodHS256,
			claims: jwt.RegisteredClaims{Subject: "1", IssuedAt: issuedAt},
		},
		{
			name:   "non-numeric subject",
			method: jwt.SigningMethodHS256,
			claims: jwt.RegisteredClaims{Subject: "alice", IssuedAt: issuedAt, ExpiresAt: validExpiration},
		},
		{
			name:   "non-positive subject",
			method: jwt.SigningMethodHS256,
			claims: jwt.RegisteredClaims{Subject: "0", IssuedAt: issuedAt, ExpiresAt: validExpiration},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, err := jwt.NewWithClaims(tt.method, tt.claims).SignedString(key)
			require.NoError(t, err)

			_, err = service.Authenticate(token)

			require.ErrorIs(t, err, ErrInvalidToken)
		})
	}
}

// Проверяет обязательность ключа, чтобы сервис не создавал неподписанные сессии.
func TestNewServiceRejectsEmptySigningKey(t *testing.T) {
	_, err := NewService(&memoryUserStore{users: make(map[string]storedUser)}, nil)

	require.Error(t, err)
}
