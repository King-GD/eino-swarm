package update

import "testing"

func TestLinuxArchiveNameMatchesItsArchitectureAndCanonicalVersion(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		if got := LinuxAssetName("v1.2.3", arch); got != "zwai-1.2.3-linux-"+arch+".tar.gz" {
			t.Fatal(got)
		}
	}
}
