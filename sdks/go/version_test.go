package flagnetic_test

import (
	"os"
	"testing"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// A Go module's version is its tag, so Version must equal the version in the
// sdks/go/vX.Y.Z tag being released: it is what every request's User-Agent
// and every summary envelope report. The release job sets
// FLAGNETIC_RELEASE_VERSION from the tag; outside a release there is no tag
// to compare.
func TestVersionIsTheReleasedTagsVersion(t *testing.T) {
	want, ok := os.LookupEnv("FLAGNETIC_RELEASE_VERSION")
	if !ok {
		t.Skip("FLAGNETIC_RELEASE_VERSION is set by the release job only")
	}
	if flagnetic.Version != want {
		t.Errorf("Version = %q, but the release tag says %q", flagnetic.Version, want)
	}
}
