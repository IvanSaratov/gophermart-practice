package order

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type orderStoreStub struct {
	ownerID int64
	created bool
	err     error
	called  bool
	userID  int64
	number  string
}

// Запоминает аргументы и возвращает настроенный результат хранилища.
func (s *orderStoreStub) CreateOrder(_ context.Context, userID int64, number string) (int64, bool, error) {
	s.called = true
	s.userID = userID
	s.number = number
	return s.ownerID, s.created, s.err
}

// Проверяет строгий цифровой формат до обращения к хранилищу.
func TestUploadRejectsMalformedNumber(t *testing.T) {
	for _, number := range []string{"", " ", "123\n", "12 3", "123a", "１２３"} {
		t.Run(number, func(t *testing.T) {
			orders := &orderStoreStub{}
			service := NewService(orders)

			_, err := service.Upload(context.Background(), 42, number)

			require.ErrorIs(t, err, ErrInvalidFormat)
			assert.False(t, orders.called)
		})
	}
}

// Проверяет контрольную сумму через публичный сценарий загрузки.
func TestUploadValidatesLuhnChecksum(t *testing.T) {
	tests := []struct {
		name       string
		number     string
		wantErr    error
		wantCalled bool
	}{
		{name: "valid", number: "12345678903", wantCalled: true},
		{name: "invalid check digit", number: "12345678904", wantErr: ErrInvalidNumber},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orders := &orderStoreStub{ownerID: 42, created: true}
			service := NewService(orders)

			_, err := service.Upload(context.Background(), 42, tt.number)

			require.ErrorIs(t, err, tt.wantErr)
			assert.Equal(t, tt.wantCalled, orders.called)
		})
	}
}

// Проверяет различение новой загрузки, повтора владельца и чужого номера.
func TestUploadMapsOwnership(t *testing.T) {
	tests := []struct {
		name       string
		ownerID    int64
		created    bool
		wantResult UploadResult
		wantErr    error
	}{
		{name: "created", ownerID: 42, created: true, wantResult: UploadCreated},
		{name: "same owner", ownerID: 42, wantResult: UploadAlreadyOwned},
		{name: "another owner", ownerID: 73, wantErr: ErrOwnedByAnotherUser},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orders := &orderStoreStub{ownerID: tt.ownerID, created: tt.created}
			service := NewService(orders)

			result, err := service.Upload(context.Background(), 42, "12345678903")

			require.ErrorIs(t, err, tt.wantErr)
			assert.Equal(t, tt.wantResult, result)
			assert.Equal(t, int64(42), orders.userID)
			assert.Equal(t, "12345678903", orders.number)
		})
	}
}

// Проверяет добавление контекста без потери исходной ошибки хранилища.
func TestUploadPropagatesStoreError(t *testing.T) {
	storeErr := errors.New("database unavailable")
	service := NewService(&orderStoreStub{err: storeErr})

	_, err := service.Upload(context.Background(), 42, "12345678903")

	require.ErrorIs(t, err, storeErr)
	assert.ErrorContains(t, err, "save order")
}
