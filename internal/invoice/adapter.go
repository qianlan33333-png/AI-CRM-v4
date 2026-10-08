package invoice

import (
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

func (s *Service) ProviderAdapter() effects.ProviderAdapter {
	adapter, _ := s.Provider.(effects.ProviderAdapter)
	if adapter == nil {
		return nil
	}
	return providerWithPreflight{s: s, adapter: adapter}
}
