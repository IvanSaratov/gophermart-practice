package bonus

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var (
	ErrInvalidAmount = errors.New("invalid amount format")
	ErrPrecision     = errors.New("amount is not representable in hundredths")
	ErrOverflow      = errors.New("amount overflows int64")
)

// Сумма в сотых долях - 100 соответствует одному баллу.
// арифметика допускает отрицательные суммы.
type Amount int64

// Ограничение исключает пробелы и специальные значения.
var decimalPattern = regexp.MustCompile(`^(-?)(0|[1-9][0-9]*)(?:\.([0-9]+))?$`)

// Переводит обычную десятичную запись баллов в сотые доли без округления.
// Например, 12.34 превращается в 1234.
func Parse(value string) (Amount, error) {
	// Делим запись на знак, целую и дробную части.
	// Если части нет, её строка пустая.
	parts := decimalPattern.FindStringSubmatch(value)
	if parts == nil {
		return 0, ErrInvalidAmount
	}

	// Нули в конце дробной части ничего не меняют
	fraction := strings.TrimRight(parts[3], "0")
	if len(fraction) > 2 {
		return 0, ErrPrecision
	}

	// Дополняем дробную часть до двух цифр и склеиваем с целой
	cents := parts[1] + parts[2] + fraction + strings.Repeat("0", 2-len(fraction))
	result, err := strconv.ParseInt(cents, 10, 64)
	if err != nil {
		// Слишком большая сумма.
		return 0, ErrOverflow
	}
	return Amount(result), nil
}

// Возвращает точную десятичную запись баллов без лишних конечных нулей.
func (a Amount) String() string {
	digits := strconv.FormatInt(int64(a), 10)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	// Не берём абсолютное значение числа - потому что модуль.
	if len(digits) < 3 {
		digits = strings.Repeat("0", 3-len(digits)) + digits
	}
	split := len(digits) - 2
	fraction := strings.TrimRight(digits[split:], "0")
	result := sign + digits[:split]
	if fraction != "" {
		result += "." + fraction
	}
	return result
}

// Складывает суммы и возвращает ошибку вместо переполнения.
func (a Amount) Add(b Amount) (Amount, error) {
	if (b > 0 && a > Amount(math.MaxInt64)-b) || (b < 0 && a < Amount(math.MinInt64)-b) {
		return 0, ErrOverflow
	}
	return a + b, nil
}

// Вычитает сумму и возвращает ошибку вместо переполнения.
func (a Amount) Sub(b Amount) (Amount, error) {
	// Проверяем границы напрямую
	if (b > 0 && a < Amount(math.MinInt64)+b) || (b < 0 && a > Amount(math.MaxInt64)+b) {
		return 0, ErrOverflow
	}
	return a - b, nil
}
