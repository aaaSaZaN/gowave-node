package updater

import (
	"runtime"
	"testing"
)

func TestMatchAssetXray(t *testing.T) {
	assets := []ReleaseAsset{
		{Name: "Xray-android-amd64.zip"},
		{Name: "Xray-android-arm64-v8a.zip"},
		{Name: "Xray-linux-32.zip"},
		{Name: "Xray-linux-64.zip"},
		{Name: "Xray-linux-arm32-v5.zip"},
		{Name: "Xray-linux-arm32-v6.zip"},
		{Name: "Xray-linux-arm32-v7a.zip"},
		{Name: "Xray-linux-arm64-v8a.zip"},
		{Name: "Xray-linux-mips32.zip"},
		{Name: "Xray-linux-mips32le.zip"},
		{Name: "Xray-linux-mips64.zip"},
		{Name: "Xray-linux-mips64le.zip"},
		{Name: "Xray-linux-riscv64.zip"},
	}

	tests := []struct {
		arch     string
		expected string
	}{
		{"amd64", "Xray-linux-64.zip"},
		{"arm64", "Xray-linux-arm64-v8a.zip"},
		{"arm", "Xray-linux-arm32-v7a.zip"},
		{"armv7", "Xray-linux-arm32-v7a.zip"},
		{"armv6", "Xray-linux-arm32-v6.zip"},
		{"armv5", "Xray-linux-arm32-v5.zip"},
		{"riscv64", "Xray-linux-riscv64.zip"},
		{"mipsle", "Xray-linux-mips32le.zip"},
		{"mips", "Xray-linux-mips32.zip"},
		{"mips64", "Xray-linux-mips64.zip"},
		{"mips64le", "Xray-linux-mips64le.zip"},
		{"386", "Xray-linux-32.zip"},
	}

	for _, tc := range tests {
		a, err := MatchAsset(assets, "linux", tc.arch, "rw-core")
		if err != nil {
			t.Fatalf("arch %s: unexpected error: %v", tc.arch, err)
		}
		if a.Name != tc.expected {
			t.Errorf("arch %s: expected %s, got %s", tc.arch, tc.expected, a.Name)
		}
	}
}

func TestMatchAssetRemnanode(t *testing.T) {
	assets := []ReleaseAsset{
		{Name: "remnanode-linux-amd64"},
		{Name: "remnanode-linux-arm64"},
		{Name: "remnanode-linux-armv5"},
		{Name: "remnanode-linux-armv6"},
		{Name: "remnanode-linux-armv7"},
		{Name: "remnanode-linux-riscv64"},
		{Name: "remnanode-linux-mips"},
		{Name: "remnanode-linux-mipsle"},
		{Name: "remnanode-linux-mips64"},
		{Name: "remnanode-linux-mips64le"},
	}

	tests := []struct {
		arch     string
		expected string
	}{
		{"amd64", "remnanode-linux-amd64"},
		{"arm64", "remnanode-linux-arm64"},
		{"arm", "remnanode-linux-armv7"},
		{"armv7", "remnanode-linux-armv7"},
		{"armv6", "remnanode-linux-armv6"},
		{"armv5", "remnanode-linux-armv5"},
		{"riscv64", "remnanode-linux-riscv64"},
		{"mipsle", "remnanode-linux-mipsle"},
		{"mips", "remnanode-linux-mips"},
		{"mips64", "remnanode-linux-mips64"},
		{"mips64le", "remnanode-linux-mips64le"},
	}

	for _, tc := range tests {
		a, err := MatchAsset(assets, "linux", tc.arch, "remnanode")
		if err != nil {
			t.Fatalf("arch %s: unexpected error: %v", tc.arch, err)
		}
		if a.Name != tc.expected {
			t.Errorf("arch %s: expected %s, got %s", tc.arch, tc.expected, a.Name)
		}
	}
}

func TestAssetArch(t *testing.T) {
	got := AssetArch()
	t.Logf("AssetArch() = %s (GOARCH %s)", got, runtime.GOARCH)
	if runtime.GOARCH == "arm" {
		if got != "armv"+armVersion() {
			t.Fatalf("AssetArch() = %s for GOARM %s", got, armVersion())
		}
	} else if got != runtime.GOARCH {
		t.Fatalf("AssetArch() = %s, want %s", got, runtime.GOARCH)
	}
}
