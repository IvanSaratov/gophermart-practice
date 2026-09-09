package store

import (
	"context"
	"crypto/sha256"
	"fmt"
)

// Возвращает порцию ожидающих заказов после указанного номера.
// Gустой номер начинает обход заново.
func (s *Store) PendingOrders(ctx context.Context, after string, limit int) ([]string, error) {
	var cursor []byte
	if after != "" {
		hash := sha256.Sum256([]byte(after))
		cursor = hash[:]
	}
	const query = `
  SELECT number FROM orders
  WHERE status IN ('NEW', 'PROCESSING')
    AND ($1::bytea IS NULL OR number_hash > $1)
  ORDER BY number_hash
  LIMIT $2
 `
	rows, err := s.pool.Query(ctx, query, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("query pending orders: %w", err)
	}
	defer rows.Close()
	numbers := make([]string, 0)
	for rows.Next() {
		var number string
		if err := rows.Scan(&number); err != nil {
			return nil, fmt.Errorf("scan pending order: %w", err)
		}
		numbers = append(numbers, number)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending orders: %w", err)
	}
	return numbers, nil
}
