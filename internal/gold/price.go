package gold

import "time"

// Price is a spot gold quote in USD per troy ounce at a point in time.
type Price struct {
	USDPerOunce float64
	At          time.Time
}
