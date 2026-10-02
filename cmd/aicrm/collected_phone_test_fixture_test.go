package main

import (
	"encoding/base64"
	"testing"

	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/secure"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
)

func collectedPhoneTestRecorder(t *testing.T) customerport.CollectedPhoneRecorder {
	t.Helper()
	vault, err := identitysecure.NewPhoneVault(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	service, err := customerapp.NewCollectedPhoneService(identityapp.OneIDService{Store: identitystore.NewPostgresStore(vault)}, customerstore.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	return service
}
