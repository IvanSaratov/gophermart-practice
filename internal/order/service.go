package order

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrInvalidFormat      = errors.New("invalid order format")
	ErrInvalidNumber      = errors.New("invalid order number")
	ErrOwnedByAnotherUser = errors.New("order belongs to another user")
)

// Описывает успешный результат загрузки номера.
type UploadResult uint8

const (
	UploadCreated UploadResult = iota
	UploadAlreadyOwned
)

// Ограничивает зависимость сервиса необходимыми операциями.
type Store interface {
	CreateOrder(context.Context, int64, string) (int64, bool, error)
	UserOrders(context.Context, int64) ([]Order, error)
}

// Объединяет пользовательские сценарии работы с заказами.
type Service struct {
	orders Store
}

// Собирает сервис вокруг хранилища заказов.
func NewService(orders Store) *Service {
	return &Service{orders: orders}
}

// Проверяет номер и различает новую загрузку, повтор владельца и чужой заказ.
func (s *Service) Upload(ctx context.Context, userID int64, number string) (UploadResult, error) {
	if !hasDigitsOnly(number) {
		return UploadCreated, ErrInvalidFormat
	}
	if !hasValidChecksum(number) {
		return UploadCreated, ErrInvalidNumber
	}

	ownerID, created, err := s.orders.CreateOrder(ctx, userID, number)
	if err != nil {
		return UploadCreated, fmt.Errorf("save order: %w", err)
	}
	if created {
		return UploadCreated, nil
	}
	if ownerID != userID {
		return UploadCreated, ErrOwnedByAnotherUser
	}

	return UploadAlreadyOwned, nil
}

// Возвращает сохранённые заказы пользователя в порядке хранилища.
func (s *Service) List(ctx context.Context, userID int64) ([]Order, error) {
	orders, err := s.orders.UserOrders(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("load user orders: %w", err)
	}

	return orders, nil
}

// Отклоняет пустые строки и любые символы кроме цифр.
func hasDigitsOnly(number string) bool {
	if number == "" {
		return false
	}
	for i := 0; i < len(number); i++ {
		if number[i] < '0' || number[i] > '9' {
			return false
		}
	}

	return true
}

// Вычисляет контрольную сумму Луна для строки цифр.
func hasValidChecksum(number string) bool {
	sum := 0
	double := false
	for i := len(number) - 1; i >= 0; i-- {
		digit := int(number[i] - '0')
		// При движении справа налево удваивается каждая вторая цифра, кроме контрольной.
		if double {
			digit *= 2
			if digit > 9 {
				digit -= 9
			}
		}
		sum += digit
		double = !double
	}

	return sum%10 == 0
}
