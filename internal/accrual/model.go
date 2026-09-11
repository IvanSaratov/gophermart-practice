package accrual

import (
	"errors"
	"fmt"
	"time"

	"github.com/ivansaratov/gophermart-practice/internal/bonus"
)

var (
	ErrNotRegistered   = errors.New("order is not registered in accrual system")
	ErrInvalidResponse = errors.New("invalid accrual response")
)

// Статус заказа во внешней системе начислений.
type Status string

const (
	StatusRegistered Status = "REGISTERED"
	StatusProcessing Status = "PROCESSING"
	StatusInvalid    Status = "INVALID"
	StatusProcessed  Status = "PROCESSED"
)

// Результат запроса начислений.
type Result struct {
	Number  string
	Status  Status
	Accrual *bonus.Amount
}

// HTTP-ошибка внешнего сервиса.=.
type HTTPError struct {
	StatusCode int
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("accrual service returned HTTP %d", e.StatusCode)
}
