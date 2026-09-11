package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/ivansaratov/gophermart-practice/internal/order"
	"go.uber.org/zap"
)

const maxOrderBodySize = 64 << 10

// Ограничивает HTTP-слой пользовательскими сценариями работы с заказами.
type Orders interface {
	Upload(context.Context, int64, string) (order.UploadResult, error)
	List(context.Context, int64) ([]order.Order, error)
}

type orderHandlers struct {
	logger *zap.Logger
	orders Orders
}

type orderResponse struct {
	Number     string          `json:"number"`
	Status     order.Status    `json:"status"`
	UploadedAt time.Time       `json:"uploaded_at"`
	Accrual    json.RawMessage `json:"accrual,omitzero"`
}

// Принимает номер и переводит доменный результат в статус.
func (h orderHandlers) uploadOrder(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxOrderBodySize))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	userID, ok := UserID(r.Context())
	if !ok {
		h.logger.Error("Authenticated request has no user ID")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	result, err := h.orders.Upload(r.Context(), userID, string(body))
	if err != nil {
		switch {
		case errors.Is(err, order.ErrInvalidFormat):
			w.WriteHeader(http.StatusBadRequest)
		case errors.Is(err, order.ErrOwnedByAnotherUser):
			w.WriteHeader(http.StatusConflict)
		case errors.Is(err, order.ErrInvalidNumber):
			w.WriteHeader(http.StatusUnprocessableEntity)
		default:
			h.logger.Error("Order upload failed", zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
		}
		return
	}

	switch result {
	case order.UploadCreated:
		w.WriteHeader(http.StatusAccepted)
	case order.UploadAlreadyOwned:
		w.WriteHeader(http.StatusOK)
	default:
		h.logger.Error("Order upload returned unknown result", zap.Uint8("result", uint8(result)))
		w.WriteHeader(http.StatusInternalServerError)
	}
}

// Отдаёт заказы владельца в JSON либо пустой ответ при отсутствии данных.
func (h orderHandlers) listOrders(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserID(r.Context())
	if !ok {
		h.logger.Error("Authenticated request has no user ID")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	orders, err := h.orders.List(r.Context(), userID)
	if err != nil {
		h.logger.Error("Order list failed", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if len(orders) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	response := make([]orderResponse, 0, len(orders))
	for _, item := range orders {
		var accrual json.RawMessage
		if item.Accrual != nil {
			// nil убирает поле, а "0" сохраняет ноль.
			accrual = json.RawMessage(item.Accrual.String())
		}
		response = append(response, orderResponse{
			Number:     item.Number,
			Status:     item.Status,
			UploadedAt: item.UploadedAt,
			Accrual:    accrual,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		// После начала записи статус изменить нельзя, но ошибка всё равно нужна в диагностике.
		h.logger.Error("Write order list failed", zap.Error(err))
	}
}
