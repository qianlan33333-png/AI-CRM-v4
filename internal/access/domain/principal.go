// Package domain contains authenticated principal values, not OAuth protocols.
package domain

import "errors"

var ErrInvalidPrincipal = errors.New("invalid principal")

type Kind string

const (
	KindAdmin    Kind = "admin"
	KindStaff    Kind = "staff"
	KindCustomer Kind = "customer"
	KindService  Kind = "service"
)

func (principal Principal) Validate() error {
	if principal.InternalID < 1 {
		return ErrInvalidPrincipal
	}
	switch principal.Kind {
	case KindAdmin, KindStaff, KindCustomer, KindService:
		return nil
	default:
		return ErrInvalidPrincipal
	}
}
