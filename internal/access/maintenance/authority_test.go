package maintenance

import (
	"os"
	"runtime"
	"testing"
)

func TestHostAuthorityRequiresActualLinuxRoot(t *testing.T) {
	authority, err := HostAuthority("fixture-operator")
	if runtime.GOOS == "linux" && os.Geteuid() == 0 {
		if err != nil || !authority.root || authority.operator != "fixture-operator" {
			t.Fatal("Linux root authority missing")
		}
	} else if err == nil || authority.root {
		t.Fatal("non-root host acquired authority")
	}
}

func TestHostAuthorityEmptyOperatorDefaultsOnlyAfterRootCheck(t *testing.T) {
	authority, err := HostAuthority("")
	if runtime.GOOS == "linux" && os.Geteuid() == 0 {
		if err != nil || authority.operator != "root" {
			t.Fatal("root fallback missing")
		}
	} else if err == nil || authority.root || authority.operator != "" {
		t.Fatal("operator input manufactured host authority")
	}
}
