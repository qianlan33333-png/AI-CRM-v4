package config

import "os"

// MaintenanceOperator is audit attribution only. HostAuthority independently
// requires an actual Linux root process; this value never grants permission.
func MaintenanceOperator() string { return os.Getenv("SUDO_USER") }
