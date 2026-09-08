package accrual

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ivansaratov/gophermart-practice/internal/bonus"
)

const maxResponseBodySize = 1 << 20

type Client struct {
	address    *url.URL
	httpClient *http.Client
}

// Проверяет адрес сервиса и создаёт клиент.
func NewClient(address string, timeout time.Duration) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(address))
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("accrual address must be an HTTP(S) URL without credentials, query or fragment")
	}
	if timeout <= 0 {
		return nil, errors.New("accrual request timeout must be positive")
	}
	return &Client{
		address: parsed,
		httpClient: &http.Client{
			Timeout: timeout,
			// Перенаправление не должно превращать одну попытку в несколько запросов.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// Запрашивает заказ и возвращает результат.
func (c *Client) GetOrder(ctx context.Context, number string) (Result, error) {
	if number == "" || strings.IndexFunc(number, func(ch rune) bool { return ch < '0' || ch > '9' }) >= 0 {
		return Result{}, errors.New("order number must contain only digits")
	}
	requestURL := c.address.JoinPath("api", "orders", number)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return Result{}, fmt.Errorf("create accrual request: %w", err)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return Result{}, fmt.Errorf("request accrual: %w", err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
		return decodeResult(response.Body, number)
	case http.StatusNoContent:
		return Result{}, ErrNotRegistered
	default:
		err := &HTTPError{StatusCode: response.StatusCode}
		if response.StatusCode == http.StatusTooManyRequests {
			err.RetryAfter = retryAfter(response.Header.Get("Retry-After"), time.Now())
		}
		return Result{}, err
	}
}

// Проверяет ответ сервиса и переводит сумму в сотые доли балла.
func decodeResult(body io.Reader, number string) (Result, error) {
	// Лишний байт нужен, чтобы отличить ответ ровно на границе от слишком большого запроса.
	data, err := io.ReadAll(io.LimitReader(body, maxResponseBodySize+1))
	if err != nil {
		return Result{}, fmt.Errorf("read accrual response: %w", err)
	}
	if len(data) > maxResponseBodySize {
		return Result{}, fmt.Errorf("%w: response body is too large", ErrInvalidResponse)
	}
	var response struct {
		Order   string          `json:"order"`
		Status  Status          `json:"status"`
		Accrual json.RawMessage `json:"accrual"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return Result{}, fmt.Errorf("%w: decode JSON: %w", ErrInvalidResponse, err)
	}
	if response.Order != number {
		return Result{}, fmt.Errorf("%w: order number mismatch", ErrInvalidResponse)
	}
	switch response.Status {
	case StatusRegistered, StatusProcessing, StatusInvalid, StatusProcessed:
	default:
		return Result{}, fmt.Errorf("%w: unknown status", ErrInvalidResponse)
	}

	result := Result{Number: response.Order, Status: response.Status}
	if len(response.Accrual) != 0 {
		// Сохраняем исходную запись JSON-числа.
		amount, err := bonus.Parse(string(response.Accrual))
		if err != nil {
			return Result{}, fmt.Errorf("%w: invalid accrual: %w", ErrInvalidResponse, err)
		}
		if amount < 0 {
			return Result{}, fmt.Errorf("%w: negative accrual", ErrInvalidResponse)
		}
		result.Accrual = &amount
	}
	return result, nil
}

// Переводит секунды или дату в задержку.
func retryAfter(value string, now time.Time) time.Duration {
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err == nil && seconds >= 0 && seconds <= math.MaxInt64/int64(time.Second) {
		return time.Duration(seconds) * time.Second
	}
	date, err := http.ParseTime(value)
	if err != nil || !date.After(now) {
		return 0
	}
	return date.Sub(now)
}
