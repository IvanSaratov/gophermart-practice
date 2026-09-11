package bonus

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Проверяет точный перевод десятичных значений в сотые доли балла.
func TestParse(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  Amount
	}{
		{"0", 0}, {"-0", 0}, {"0.01", 1}, {"500.5", 50050}, {"42", 4200},
		{"-1.25", -125}, {"1.2300", 123}, {"1.0000", 100},
		{"0.0000", 0}, {"-0.0000", 0}, {"12.3", 1230}, {"12.34", 1234},
		{"92233720368547758.07", Amount(math.MaxInt64)},
		{"-92233720368547758.08", Amount(math.MinInt64)},
		{"1.23" + strings.Repeat("0", 1000), 123},
	} {
		t.Run(tt.input, func(t *testing.T) {
			got, err := Parse(tt.input)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// Проверяет отказ без округления и различение синтаксиса, точности и переполнения.
func TestParseRejectsInvalidAmount(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  error
	}{
		{"", ErrInvalidAmount}, {" 1", ErrInvalidAmount}, {"1\n", ErrInvalidAmount},
		{"+1", ErrInvalidAmount}, {"01", ErrInvalidAmount}, {".5", ErrInvalidAmount},
		{"1.", ErrInvalidAmount}, {"1e", ErrInvalidAmount}, {"1/2", ErrInvalidAmount},
		{"NaN", ErrInvalidAmount}, {"Inf", ErrInvalidAmount}, {"１", ErrInvalidAmount},
		{"0.001", ErrPrecision}, {"-0.001", ErrPrecision}, {"1.2301", ErrPrecision},
		{"1e2", ErrInvalidAmount}, {"1E+2", ErrInvalidAmount}, {"1e-2", ErrInvalidAmount},
		{"0e0", ErrInvalidAmount}, {"1e999999999999999999999999", ErrInvalidAmount},
		{"92233720368547758.08", ErrOverflow}, {"-92233720368547758.09", ErrOverflow},
		{"92233720368547759", ErrOverflow}, {strings.Repeat("9", 1000), ErrOverflow},
		{"1.2300001", ErrPrecision}, {"1.2.3", ErrInvalidAmount}, {"-", ErrInvalidAmount},
	} {
		t.Run(tt.input, func(t *testing.T) {
			got, err := Parse(tt.input)
			require.ErrorIs(t, err, tt.want)
			require.Zero(t, got)
		})
	}
}

// Проверяет компактную десятичную запись, включая минимальное знаковое значение.
func TestAmountString(t *testing.T) {
	for _, tt := range []struct {
		amount Amount
		want   string
	}{
		{0, "0"}, {1, "0.01"}, {-1, "-0.01"}, {10, "0.1"}, {100, "1"},
		{101, "1.01"}, {50050, "500.5"}, {-125, "-1.25"},
		{Amount(math.MaxInt64), "92233720368547758.07"},
		{Amount(math.MinInt64), "-92233720368547758.08"},
	} {
		require.Equal(t, tt.want, tt.amount.String())
	}
}

// Проверяет сложение и вычитание на границах диапазона без перехода через переполнение.
func TestAmountArithmetic(t *testing.T) {
	for _, tt := range []struct {
		name     string
		a, b     Amount
		subtract bool
		want     Amount
		wantErr  error
	}{
		{name: "add cents", a: 10, b: 20, want: 30},
		{name: "subtract cents", a: 30, b: 20, subtract: true, want: 10},
		{name: "add negative", a: 10, b: -20, want: -10},
		{name: "subtract negative", a: 10, b: -20, subtract: true, want: 30},
		{name: "add to maximum", a: math.MaxInt64 - 1, b: 1, want: math.MaxInt64},
		{name: "add to minimum", a: math.MinInt64 + 1, b: -1, want: math.MinInt64},
		{name: "add overflow", a: math.MaxInt64, b: 1, wantErr: ErrOverflow},
		{name: "add underflow", a: math.MinInt64, b: -1, wantErr: ErrOverflow},
		{name: "add opposite limits", a: math.MaxInt64, b: math.MinInt64, want: -1},
		{name: "subtract overflow", a: math.MaxInt64, b: -1, subtract: true, wantErr: ErrOverflow},
		{name: "subtract underflow", a: math.MinInt64, b: 1, subtract: true, wantErr: ErrOverflow},
		{name: "subtract minimum", a: 0, b: math.MinInt64, subtract: true, wantErr: ErrOverflow},
		{name: "subtract equal minimum", a: math.MinInt64, b: math.MinInt64, subtract: true, want: 0},
		{name: "subtract to maximum", a: -1, b: math.MinInt64, subtract: true, want: math.MaxInt64},
		{name: "subtract to minimum", a: -1, b: math.MaxInt64, subtract: true, want: math.MinInt64},
		{name: "add zero", a: math.MinInt64, want: math.MinInt64},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got Amount
			var err error
			if tt.subtract {
				got, err = tt.a.Sub(tt.b)
			} else {
				got, err = tt.a.Add(tt.b)
			}
			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.want, got)
		})
	}
}
