package config

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// SidebarWarehouse uses an existing protected operator account without
// provisioning a user or granting a role. Read and manual generation are
// separately enabled; neither path sends a WeCom message.
type SidebarWarehouse struct {
	Enabled           bool
	GenerationEnabled bool
	Origin            string
	Username          string
	Password          string
	// Test injection only. Load never sets a custom trust/transport client.
	HTTPClient *http.Client
}

func loadSidebarWarehouse() (SidebarWarehouse, error) {
	c := SidebarWarehouse{Origin: os.Getenv("AICRM_SIDEBAR_WAREHOUSE_ORIGIN"), Username: os.Getenv("AICRM_SIDEBAR_WAREHOUSE_BASIC_USER"), Password: os.Getenv("AICRM_SIDEBAR_WAREHOUSE_BASIC_PASSWORD")}
	var err error
	if c.Enabled, err = strictBool("AICRM_SIDEBAR_WAREHOUSE_ENABLED", false); err != nil {
		return c, err
	}
	if c.GenerationEnabled, err = strictBool("AICRM_SIDEBAR_GENERATION_ENABLED", false); err != nil {
		return c, err
	}
	return c, c.Validate()
}

func (c SidebarWarehouse) Validate() error {
	if c.GenerationEnabled && !c.Enabled {
		return errors.New("sidebar generation requires the protected warehouse reader")
	}
	if !c.Enabled {
		return nil
	}
	u, err := url.Parse(c.Origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") || c.Username == "" || strings.TrimSpace(c.Username) != c.Username || strings.ContainsAny(c.Username, ":\r\n") || c.Password == "" {
		return errors.New("invalid protected sidebar warehouse configuration")
	}
	return nil
}
