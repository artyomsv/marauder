package genericmagnet

import (
	"testing"

	"github.com/artyomsv/marauder/backend/internal/plugins/e2etest"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

func TestE2E(t *testing.T) {
	e2etest.RunFullPipeline(t, e2etest.Case{
		Name: "genericmagnet/magnet-uri-roundtrips",
		Setup: func(_ *testing.T, _ *e2etest.QBitFake) (registry.Tracker, string) {
			// Generic magnet doesn't talk to a server. The "topic URL"
			// IS the magnet URI.
			magnet := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=marauder-e2e"
			return &plugin{}, magnet
		},
		ExpectedNameContains: "marauder-e2e",
	})
}
