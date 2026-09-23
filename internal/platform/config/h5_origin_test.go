package config

import "testing"

func TestH5OriginIndependentFromAdminOrigin(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://test:test@localhost/test")
	t.Setenv("AICRM_PUBLIC_ORIGIN", "https://id-dev.youcangogogo.com")
	t.Setenv("AICRM_H5_PUBLIC_ORIGIN", "https://www.qianlan333.cloud")
	cfg, err := Load()
	if err != nil || cfg.H5PublicOrigin != "https://www.qianlan333.cloud" || cfg.PublicOrigin != "https://id-dev.youcangogogo.com" {
		t.Fatalf("origin configuration err=%v", err)
	}
	t.Setenv("AICRM_H5_PUBLIC_ORIGIN", "")
	cfg, err = Load()
	if err != nil || cfg.H5PublicOrigin != cfg.PublicOrigin {
		t.Fatalf("default H5 origin err=%v", err)
	}
	t.Setenv("AICRM_H5_PUBLIC_ORIGIN", "http://www.qianlan333.cloud")
	if _, err = Load(); err == nil {
		t.Fatal("insecure H5 origin accepted")
	}
}
