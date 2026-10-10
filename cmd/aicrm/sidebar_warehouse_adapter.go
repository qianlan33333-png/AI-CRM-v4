package main

import (
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/sidebar"
)

func composeSidebarWarehouse(cfg platformconfig.SidebarWarehouse) (sidebar.AIWarehouseReader, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, nil
	}
	return sidebar.NewHTTPWarehouseReader(cfg.Origin, cfg.Username, cfg.Password, cfg.HTTPClient)
}

func composeSidebarSuggestions(cfg platformconfig.SidebarWarehouse) (sidebar.AISuggestionGenerator, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if !cfg.GenerationEnabled {
		return nil, nil
	}
	return sidebar.NewHTTPWarehouseGenerator(cfg.Origin, cfg.Username, cfg.Password, cfg.HTTPClient)
}
