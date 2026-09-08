package accrual

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ivansaratov/gophermart-practice/internal/bonus"
	"github.com/stretchr/testify/require"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testClient(t *testing.T, transport transportFunc) *Client {
	t.Helper()
	client, err := NewClient("http://accrual.test", time.Second)
	require.NoError(t, err)
	client.httpClient.Transport = transport
	return client
}

// Проверяет путь запроса, статусы и точное представление отсутствующего, нулевого и дробного начисления.
func TestGetOrder(t *testing.T) {
	for _, tt := range []struct {
		name   string
		body   string
		status Status
		amount *bonus.Amount
	}{
		{"registered", `{"order":"12345678903","status":"REGISTERED"}`, StatusRegistered, nil},
		{"processing", `{"order":"12345678903","status":"PROCESSING"}`, StatusProcessing, nil},
		{"invalid", `{"order":"12345678903","status":"INVALID"}`, StatusInvalid, nil},
		{"no accrual", `{"order":"12345678903","status":"PROCESSED"}`, StatusProcessed, nil},
		{"zero", `{"order":"12345678903","status":"PROCESSED","accrual":0}`, StatusProcessed, new(bonus.Amount(0))},
		{"fraction", `{"order":"12345678903","status":"PROCESSED","accrual":500.5000}`, StatusProcessed, new(bonus.Amount(50050))},
		{"large exact amount", `{"order":"12345678903","status":"PROCESSED","accrual":92233720368547758.07}`, StatusProcessed, new(bonus.Amount(9223372036854775807))},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			client := testClient(t, func(r *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "http://accrual.test/api/orders/12345678903", r.URL.String())
				require.Nil(t, r.Body)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tt.body))}, nil
			})
			got, err := client.GetOrder(t.Context(), "12345678903")
			require.NoError(t, err)
			require.Equal(t, Result{Number: "12345678903", Status: tt.status, Accrual: tt.amount}, got)
			require.Equal(t, 1, calls)
		})
	}
}

// Проверяет, что повреждённый ответ не превращается в результат расчёта.
func TestGetOrderRejectsInvalidResponse(t *testing.T) {
	for _, body := range []string{
		`{`, `null`, `[]`, `{}`, `{"order":"other","status":"PROCESSED"}`,
		`{"order":"12345678903","status":"UNKNOWN"}`,
		`{"order":"12345678903","status":"PROCESSED","accrual":-1}`,
		`{"order":"12345678903","status":"PROCESSED","accrual":0.001}`,
		`{"order":"12345678903","status":"PROCESSED","accrual":1e2}`,
		`{"order":"12345678903","status":"PROCESSED","accrual":"500"}`,
		`{"order":"12345678903","status":"PROCESSED","accrual":null}`,
		`{"order":"12345678903","status":"PROCESSED","accrual":92233720368547758.08}`,
		`{"order":"12345678903","status":"PROCESSED"} {}`,
	} {
		t.Run(body, func(t *testing.T) {
			client := testClient(t, func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			got, err := client.GetOrder(t.Context(), "12345678903")
			require.ErrorIs(t, err, ErrInvalidResponse)
			require.Equal(t, Result{}, got)
		})
	}
}

// Проверяет различение отсутствующего заказа и ошибок без автоматических повторов.
func TestGetOrderHTTPStatus(t *testing.T) {
	for _, status := range []int{204, 429, 500, 503, 404, 302} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			client := testClient(t, func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"60"}, "Location": {"http://redirect.test"}}, Body: io.NopCloser(strings.NewReader("service response"))}, nil
			})
			got, err := client.GetOrder(t.Context(), "12345678903")
			require.Equal(t, Result{}, got)
			require.Equal(t, 1, calls)
			if status == 204 {
				require.ErrorIs(t, err, ErrNotRegistered)
				return
			}
			var httpErr *HTTPError
			require.ErrorAs(t, err, &httpErr)
			require.Equal(t, status, httpErr.StatusCode)
			if status == 429 {
				require.Equal(t, time.Minute, httpErr.RetryAfter)
			} else {
				require.Zero(t, httpErr.RetryAfter)
			}
		})
	}
}

// Проверяет секунды и дату.
func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		value string
		want  time.Duration
	}{
		{"60", time.Minute}, {"0", 0}, {"", 0}, {"invalid", 0}, {"-1", 0},
		{"9223372036854775807", 0},
		{"Tue, 08 Sep 2026 12:01:00 GMT", time.Minute},
		{"Tue, 08 Sep 2026 11:59:00 GMT", 0},
	} {
		require.Equal(t, tt.want, retryAfter(tt.value, now), tt.value)
	}
}

// Проверяет ограничение времени запроса.
func TestGetOrderRequestErrors(t *testing.T) {
	networkErr := errors.New("connection failed")
	client := testClient(t, func(*http.Request) (*http.Response, error) { return nil, networkErr })
	_, err := client.GetOrder(t.Context(), "12345678903")
	require.ErrorIs(t, err, networkErr)

	client = testClient(t, func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = client.GetOrder(ctx, "12345678903")
	require.ErrorIs(t, err, context.Canceled)

	client.httpClient.Timeout = 10 * time.Millisecond
	_, err = client.GetOrder(t.Context(), "12345678903")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// Запоминает закрытие тела ответа и число прочитанных байтов.
type trackedBody struct {
	io.Reader
	closed bool
	read   int
}

// Подсчитывает прочитанные байты для проверки ограничения размера ответа.
func (b *trackedBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}

func (b *trackedBody) Close() error { b.closed = true; return nil }

// Проверяет закрытие ответа и прекращение чтения слишком большого тела.
func TestGetOrderClosesBody(t *testing.T) {
	for _, tt := range []struct {
		name    string
		status  int
		body    string
		wantErr bool
	}{
		{"success", 200, `{"order":"12345678903","status":"PROCESSED"}`, false},
		{"at size limit", 200, `{"order":"12345678903","status":"PROCESSED"}` + strings.Repeat(" ", maxResponseBodySize-len(`{"order":"12345678903","status":"PROCESSED"}`)), false},
		{"bad JSON", 200, `{`, true},
		{"no order", 204, "", true},
		{"HTTP error", 500, "error", true},
		{"oversized", 200, strings.Repeat(" ", maxResponseBodySize+100), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := &trackedBody{Reader: strings.NewReader(tt.body)}
			client := testClient(t, func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tt.status, Body: body}, nil
			})
			_, err := client.GetOrder(t.Context(), "12345678903")
			require.Equal(t, tt.wantErr, err != nil)
			require.True(t, body.closed)
			require.LessOrEqual(t, body.read, maxResponseBodySize+1)
		})
	}
}

// Проверяет параметры клиента до попытки отправить запрос.
func TestNewClientRejectsInvalidConfiguration(t *testing.T) {
	for _, address := range []string{"", "localhost:8080", "ftp://host", "http://", "http://host:bad", "http://host?x=1", "http://host#x", "http://user:secret@host"} {
		_, err := NewClient(address, time.Second)
		require.Error(t, err, address)
		require.NotContains(t, err.Error(), "secret")
	}
	for _, timeout := range []time.Duration{0, -time.Second} {
		_, err := NewClient("http://accrual.test", timeout)
		require.Error(t, err)
	}
}

type failingReader struct{ err error }

// Имитирует обрыв чтения тела ответа.
func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

// Проверяет сохранение ошибки чтения и освобождение ответа после обрыва.
func TestGetOrderReadError(t *testing.T) {
	readErr := errors.New("response interrupted")
	body := &trackedBody{Reader: failingReader{err: readErr}}
	client := testClient(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
	})
	got, err := client.GetOrder(t.Context(), "12345678903")
	require.ErrorIs(t, err, readErr)
	require.Equal(t, Result{}, got)
	require.True(t, body.closed)
}

// Проверяет, что некорректный номер не может изменить путь запроса к сервису.
func TestGetOrderRejectsInvalidNumber(t *testing.T) {
	called := false
	client := testClient(t, func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("unexpected request")
	})
	for _, number := range []string{"", "../orders", "123?x=1", "１２３", "123\n"} {
		_, err := client.GetOrder(t.Context(), number)
		require.Error(t, err)
	}
	require.False(t, called)
}

// Проверяет сохранение префикса адреса и ведущих нулей номера.
func TestGetOrderPreservesAddressPrefixAndNumber(t *testing.T) {
	client, err := NewClient("http://accrual.test/prefix/", time.Second)
	require.NoError(t, err)
	client.httpClient.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "/prefix/api/orders/00123", r.URL.Path)
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	_, err = client.GetOrder(t.Context(), "00123")
	require.ErrorIs(t, err, ErrNotRegistered)
}
