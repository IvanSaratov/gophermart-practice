package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidInput       = errors.New("invalid credentials input")
	ErrLoginTaken         = errors.New("login already taken")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidToken       = errors.New("invalid token")
)

const SessionTTL = 24 * time.Hour

// Описывает подписанную пользовательскую сессию и момент её завершения.
type Session struct {
	Token     string
	ExpiresAt time.Time
}

// Ограничивает зависимость сервиса только операциями с учётными данными.
type UserStore interface {
	CreateUser(context.Context, string, string) (int64, bool, error)
	UserCredentials(context.Context, string) (int64, string, bool, error)
}

// Управляет учётными данными и подписанными пользовательскими сессиями.
type Service struct {
	users      UserStore
	signingKey []byte
	// Нужен для быстрого ответа + исключения дорогой операции bcrypt
	// так же если не отвечать быстро - то можно замерить что логина такого не существует.
	dummyPasswordHash []byte
	now               func() time.Time
}

// Собирает сервис с собственной копией ключа.
func NewService(users UserStore, signingKey []byte) (*Service, error) {
	if users == nil {
		return nil, errors.New("user store is required")
	}
	if len(signingKey) == 0 {
		return nil, errors.New("signing key is required")
	}
	dummyPasswordHash, err := bcrypt.GenerateFromPassword([]byte("invalid-credentials"), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("prepare dummy password hash: %w", err)
	}

	return &Service{
		users:             users,
		signingKey:        append([]byte(nil), signingKey...),
		dummyPasswordHash: dummyPasswordHash,
		now:               time.Now,
	}, nil
}

// Создаёт учётную запись с bcrypt-хешем и сразу открывает пользовательскую сессию.
func (s *Service) Register(ctx context.Context, login, password string) (Session, error) {
	if login == "" || password == "" {
		return Session{}, ErrInvalidInput
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		if errors.Is(err, bcrypt.ErrPasswordTooLong) {
			return Session{}, ErrInvalidInput
		}
		return Session{}, fmt.Errorf("hash password: %w", err)
	}

	userID, created, err := s.users.CreateUser(ctx, login, string(passwordHash))
	if err != nil {
		return Session{}, fmt.Errorf("create user: %w", err)
	}
	if !created {
		return Session{}, ErrLoginTaken
	}

	return s.issueSession(userID)
}

// Проверяет пару логин-пароль и открывает новую сессию без раскрытия ошибочного поля.
func (s *Service) Login(ctx context.Context, login, password string) (Session, error) {
	if login == "" || password == "" {
		return Session{}, ErrInvalidInput
	}

	userID, passwordHash, found, err := s.users.UserCredentials(ctx, login)
	if err != nil {
		return Session{}, fmt.Errorf("load user credentials: %w", err)
	}
	candidateHash := []byte(passwordHash)
	if !found {
		// Одинаково дорогая проверка не позволяет отличить неизвестный логин по времени ответа.
		candidateHash = s.dummyPasswordHash
	}
	if err := bcrypt.CompareHashAndPassword(candidateHash, []byte(password)); err != nil {
		if !found {
			return Session{}, ErrInvalidCredentials
		}
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return Session{}, ErrInvalidCredentials
		}
		return Session{}, fmt.Errorf("compare password hash: %w", err)
	}
	if !found {
		return Session{}, ErrInvalidCredentials
	}

	return s.issueSession(userID)
}
