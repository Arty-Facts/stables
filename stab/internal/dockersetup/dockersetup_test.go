package dockersetup

import "testing"

func TestInstallInstructions(t *testing.T) {
	if InstallInstructions(true) == InstallInstructions(false) {
		t.Fatal("nvidia and regular instructions must differ")
	}
	if InstallInstructions(false) != regularSetup {
		t.Fatal("regular instructions wrong")
	}
	if InstallInstructions(true) != nvidiaSetup {
		t.Fatal("nvidia instructions wrong")
	}
	if len(regularSetup) == 0 || len(nvidiaSetup) == 0 {
		t.Fatal("instructions must not be empty")
	}
}
