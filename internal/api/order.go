package api

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/ivansaratov/gophermart-practice/internal/order"
	"go.uber.org/zap"
)

const maxOrderBodySize = 64 << 10

// Ограничивает HTTP-слой единственным сценарием загрузки заказа.
type OrderUpload interface {
	Upload(context.Context, int64, string) (order.UploadResult, error)
}

type orderHandlers struct {
	logger *zap.Logger
	upload OrderUpload
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

	result, err := h.upload.Upload(r.Context(), userID, string(body))
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
