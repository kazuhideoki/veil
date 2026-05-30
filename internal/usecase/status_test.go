package usecase

import (
	"testing"
	"time"
)

func TestFormatTTLRemainingKeepsTwoDigitHoursBelowOneHour(t *testing.T) {
	got := formatTTLRemaining(48*time.Minute + 32*time.Second)
	if got != "00h48m32s" {
		t.Fatalf("formatTTLRemaining() = %q, want %q", got, "00h48m32s")
	}
}

func TestFormatTTLRemainingUsesTwoDigitClockFields(t *testing.T) {
	got := formatTTLRemaining(2*time.Hour + 3*time.Minute + 4*time.Second)
	if got != "02h03m04s" {
		t.Fatalf("formatTTLRemaining() = %q, want %q", got, "02h03m04s")
	}
}
