package updater

import "testing"

func TestNeedsUpdate_Satisfied(t *testing.T) {
	needs, err := NeedsUpdate("1.2.3", ">=1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if needs {
		t.Error("1.2.3 satisfies >=1.0.0, should not need update")
	}
}

func TestNeedsUpdate_NotSatisfied(t *testing.T) {
	needs, err := NeedsUpdate("1.0.0", ">=1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if !needs {
		t.Error("1.0.0 does not satisfy >=1.2.0, should need update")
	}
}

func TestNeedsUpdate_DevVersion(t *testing.T) {
	needs, err := NeedsUpdate("dev", ">=1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if needs {
		t.Error("dev version should skip update check")
	}
}

func TestNeedsUpdate_EmptyConstraint(t *testing.T) {
	needs, err := NeedsUpdate("1.0.0", "")
	if err != nil {
		t.Fatal(err)
	}
	if needs {
		t.Error("empty constraint should not need update")
	}
}

func TestNeedsUpdate_ComplexConstraint(t *testing.T) {
	needs, err := NeedsUpdate("2.0.0", ">=1.0.0, <2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if !needs {
		t.Error("2.0.0 does not satisfy >=1.0.0, <2.0.0")
	}
}

func TestAssetName(t *testing.T) {
	tests := []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "webda-cli-linux-amd64"},
		{"darwin", "arm64", "webda-cli-darwin-arm64"},
		{"windows", "amd64", "webda-cli-windows-amd64.exe"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := AssetName(tt.goos, tt.goarch)
			if got != tt.want {
				t.Errorf("AssetName(%q, %q) = %q, want %q", tt.goos, tt.goarch, got, tt.want)
			}
		})
	}
}
