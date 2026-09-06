package auth

import (
	"crypto/rand"
	"fmt"
	"strconv"

	"github.com/golang-jwt/jwt/v5"
)

const signingKeySize = 32

// Генерирует криптографически случайный ключ для запуска если секрет не был передан.
func GenerateSigningKey() ([]byte, error) {
	key := make([]byte, signingKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate signing key: %w", err)
	}

	return key, nil
}

// Подписывает новую сессию с идентификатором пользователя и ограниченным сроком жизни.
func (s *Service) issueSession(userID int64) (Session, error) {
	issuedAt := s.now()
	expiresAt := issuedAt.Add(SessionTTL)
	claims := jwt.RegisteredClaims{
		Subject:   strconv.FormatInt(userID, 10),
		IssuedAt:  jwt.NewNumericDate(issuedAt),
		ExpiresAt: jwt.NewNumericDate(expiresAt),
	}

	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.signingKey)
	if err != nil {
		return Session{}, fmt.Errorf("sign session token: %w", err)
	}

	return Session{Token: token, ExpiresAt: expiresAt}, nil
}

// Проверяет подпись и срок сессии и извлекает положительный идентификатор пользователя.
func (s *Service) Authenticate(tokenValue string) (int64, error) {
	claims := new(jwt.RegisteredClaims)
	token, err := jwt.ParseWithClaims(
		tokenValue,
		claims,
		func(_ *jwt.Token) (any, error) {
			return s.signingKey, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithTimeFunc(s.now),
	)
	if err != nil || !token.Valid {
		return 0, ErrInvalidToken
	}

	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || userID <= 0 {
		return 0, ErrInvalidToken
	}

	return userID, nil
}
