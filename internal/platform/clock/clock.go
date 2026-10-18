package clock

import "time"

// Clock supplies the current instant. Tests use a fixed clock.
type Clock interface {
	Now() time.Time
}

// Real returns the current UTC time.
type Real struct{}

// Now returns the current UTC time.
func (Real) Now() time.Time {
	return time.Now().UTC()
}

// Fixed always returns the same instant.
type Fixed time.Time

// Now returns the fixed instant in UTC.
func (f Fixed) Now() time.Time {
	return time.Time(f).UTC()
}
