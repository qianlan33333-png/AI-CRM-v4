package wecom

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

type SidebarPrincipal struct {
	CorpID     string
	EmployeeID string
}

type SidebarPrincipalResolver interface {
	// sessionToken is read exclusively from the HttpOnly sidebar cookie by the
	// HTTP adapter. Implementations must not derive this principal from request
	// headers or caller-provided employee/corporation fields.
	SidebarPrincipal(context.Context, string) (SidebarPrincipal, error)
}

type ContextTokenService struct {
	CorpID     string
	SigningKey []byte
	TTL        time.Duration
	Now        func() time.Time
}

type contextClaims struct {
	CorpID     string `json:"corp_id"`
	EmployeeID string `json:"employee_id"`
	CustomerID int64  `json:"customer_id"`
	ExpiresAt  int64  `json:"exp"`
}

func (service ContextTokenService) Issue(ctx context.Context, principal SidebarPrincipal, customerID customerdomain.CustomerID) (string, error) {
	if principal.CorpID != service.CorpID || principal.EmployeeID == "" || customerID < 1 || !service.valid() {
		return "", ErrInvalidContext
	}
	ttl := service.TTL
	if ttl <= 0 || ttl > 15*time.Minute {
		ttl = 5 * time.Minute
	}
	claims := contextClaims{CorpID: principal.CorpID, EmployeeID: principal.EmployeeID, CustomerID: int64(customerID), ExpiresAt: service.clock()().Add(ttl).Unix()}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", ErrInvalidContext
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, service.SigningKey)
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (service ContextTokenService) Verify(ctx context.Context, token string) (SidebarPrincipal, customerdomain.CustomerID, error) {
	if !service.valid() {
		return SidebarPrincipal{}, 0, ErrInvalidContext
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return SidebarPrincipal{}, 0, ErrInvalidContext
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return SidebarPrincipal{}, 0, ErrInvalidContext
	}
	mac := hmac.New(sha256.New, service.SigningKey)
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return SidebarPrincipal{}, 0, ErrInvalidContext
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return SidebarPrincipal{}, 0, ErrInvalidContext
	}
	var claims contextClaims
	if json.Unmarshal(payload, &claims) != nil || claims.CorpID != service.CorpID || claims.EmployeeID == "" || claims.CustomerID < 1 || claims.ExpiresAt < service.clock()().Unix() {
		return SidebarPrincipal{}, 0, ErrInvalidContext
	}
	principal := SidebarPrincipal{CorpID: claims.CorpID, EmployeeID: claims.EmployeeID}
	customerID := customerdomain.CustomerID(claims.CustomerID)
	return principal, customerID, nil
}

func (service ContextTokenService) valid() bool {
	return service.CorpID != "" && len(service.SigningKey) >= 32
}

func (service ContextTokenService) clock() func() time.Time {
	if service.Now != nil {
		return service.Now
	}
	return func() time.Time { return time.Now().UTC() }
}
