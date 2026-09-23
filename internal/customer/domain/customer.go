// Package domain contains the minimal channel-neutral Customer root.
package domain

import (
	"errors"
	"strconv"
	"strings"
)

var ErrInvalidCustomer = errors.New("invalid customer")

type CustomerID int64
type Status string

const (
	StatusActive Status = "active"
	StatusMerged Status = "merged"
	StatusClosed Status = "closed"
)

type Customer struct {
	ID     CustomerID
	Status Status
}

// CanonicalOneIDLabel is the stable, presentation-safe spelling of a
// canonical Customer root. It derives from the root key, never a mutable
// directory cache value.
func CanonicalOneIDLabel(customerID CustomerID) string {
	if customerID < 1 {
		return ""
	}
	return "CID-" + strconv.FormatInt(int64(customerID), 10)
}

// ParseCanonicalOneIDLabel accepts only the exact canonical presentation
// spelling. It is a parser, not an identity resolver or a customer creator.
func ParseCanonicalOneIDLabel(value string) (CustomerID, bool) {
	if !strings.HasPrefix(value, "CID-") {
		return 0, false
	}
	numeric := strings.TrimPrefix(value, "CID-")
	if numeric == "" || numeric[0] == '0' {
		return 0, false
	}
	for _, character := range numeric {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.ParseInt(numeric, 10, 64)
	if err != nil || parsed < 1 {
		return 0, false
	}
	return CustomerID(parsed), true
}

func (customer Customer) Validate() error {
	if customer.ID < 1 {
		return ErrInvalidCustomer
	}
	switch customer.Status {
	case StatusActive, StatusMerged, StatusClosed:
		return nil
	default:
		return ErrInvalidCustomer
	}
}
