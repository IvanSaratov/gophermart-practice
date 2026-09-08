package order

import "time"

// Состояние обработки заказа.
type Status string

// Дублируем из БД
const (
	StatusNew        Status = "NEW"
	StatusProcessing Status = "PROCESSING"
	StatusInvalid    Status = "INVALID"
	StatusProcessed  Status = "PROCESSED"
)

// Данные заказа, доступные его владельцу.
type Order struct {
	Number     string
	Status     Status
	UploadedAt time.Time
}
