package app

import (
	"context"
	"errors"
	"strings"
	"time"

	releaseport "github.com/qianlan33333-png/AI-CRM-v3/internal/release/port"
)

var ErrInvalidObservation = errors.New("invalid release observation")

// ObservationService is the release-plane adapter for truthful local release
// facts. It has no deployer, switcher, Provider, or external-effect client.
type ObservationService struct {
	writer releaseport.ObservationWriter
}

func NewObservationService(writer releaseport.ObservationWriter) (*ObservationService, error) {
	if writer == nil {
		return nil, ErrInvalidObservation
	}
	return &ObservationService{writer: writer}, nil
}

func (service *ObservationService) Record(ctx context.Context, observation releaseport.ReleaseObservation) error {
	if service == nil || service.writer == nil || !validObservation(observation) {
		return ErrInvalidObservation
	}
	if observation.ObservedAt.IsZero() {
		observation.ObservedAt = time.Now().UTC()
	}
	return service.writer.RecordReleaseObservation(ctx, observation)
}

func validObservation(observation releaseport.ReleaseObservation) bool {
	if observation.ReleaseSHA == "" || len(observation.ReleaseSHA) > 200 || strings.TrimSpace(observation.ReleaseSHA) != observation.ReleaseSHA {
		return false
	}
	for _, character := range observation.ReleaseSHA {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && !strings.ContainsRune("._:/-", character) {
			return false
		}
	}
	normalized := strings.ToLower(strings.NewReplacer("_", "", ".", "", ":", "", "/", "", "-", "").Replace(observation.ReleaseSHA))
	for _, forbidden := range []string{"secret", "token", "password", "cookie", "privatekey", "openid", "externaluserid", "phone", "mobile", "email", "apikey", "authorization", "credential"} {
		if strings.Contains(normalized, forbidden) {
			return false
		}
	}
	switch observation.Status {
	case "observed", "active", "superseded", "failed":
		return true
	default:
		return false
	}
}
