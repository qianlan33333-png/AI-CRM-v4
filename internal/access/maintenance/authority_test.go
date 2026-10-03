package maintenance

import (
	"os"
	"runtime"
	"testing"
)

func TestHostAuthorityRequiresActualLinuxRoot(t *testing.T) {
	authority, err := HostAuthority()
	if runtime.GOOS == "linux" && os.Geteuid() == 0 {
		if err != nil || !authority.root {
			t.Fatal("Linux root authority missing")
		}
	} else if err == nil || authority.root {
		t.Fatal("non-root host acquired authority")
	}
}
