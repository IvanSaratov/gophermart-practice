package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/ivansaratov/gophermart-practice/internal/bonus"
	"github.com/ivansaratov/gophermart-practice/internal/order"
)

var errInvalidAccrualResult = errors.New("invalid accrual result")

// Сохраняет результат расчёта и начисляет баллы одной транзакцией.
func (s *Store) ApplyAccrual(ctx context.Context, number string, status order.Status, amount *bonus.Amount) error {
	switch status {
	case order.StatusProcessing, order.StatusInvalid, order.StatusProcessed:
	default:
		return errInvalidAccrualResult
	}
	var accrual any
	var credit int64
	if amount != nil {
		credit = int64(*amount)
		if credit < 0 || status != order.StatusProcessed {
			return errInvalidAccrualResult
		}
		// nil сохраняется как NULL в SQL, а явно полученный ноль как число 0.
		accrual = credit
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin accrual transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	hash := sha256.Sum256([]byte(number))
	const selectQuery = `
        SELECT number, user_id, status FROM orders
        WHERE number_hash = $1 FOR UPDATE
    `
	var storedNumber string
	var userID int64
	var current order.Status
	// Другой обработчик этого заказа ждёт здесь.
	// Для предотвращения гонки.
	if err := tx.QueryRow(ctx, selectQuery, hash[:]).Scan(&storedNumber, &userID, &current); err != nil {
		return fmt.Errorf("lock accrual order: %w", err)
	}
	if storedNumber != number {
		return errOrderHashCollision
	}
	if current == order.StatusProcessed || current == order.StatusInvalid {
		return nil
	}

	const updateQuery = `UPDATE orders SET status = $1, accrual = $2 WHERE number_hash = $3`
	if _, err := tx.Exec(ctx, updateQuery, status, accrual, hash[:]); err != nil {
		return fmt.Errorf("update accrual order: %w", err)
	}
	if credit > 0 {
		const creditQuery = `UPDATE users SET balance = balance + $1 WHERE id = $2`
		if _, err := tx.Exec(ctx, creditQuery, credit, userID); err != nil {
			return fmt.Errorf("credit user balance: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit accrual transaction: %w", err)
	}
	return nil
}
