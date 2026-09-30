package pulumistep

import (
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
)

func TestHasOutputsToSync(t *testing.T) {
	if hasOutputsToSync(OutputsToMap(auto.OutputMap{})) {
		t.Fatal("a never-deployed stack has nothing to sync on a preview")
	}

	onlySecret := OutputsToMap(auto.OutputMap{"token": {Value: "x", Secret: true}})
	if hasOutputsToSync(onlySecret) {
		t.Fatal("secrets are filtered before the hook; a stack exporting only secrets has nothing to sync")
	}

	if !hasOutputsToSync(OutputsToMap(auto.OutputMap{"bucketName": {Value: "b"}})) {
		t.Fatal("a deployed stack's outputs must reach the hook")
	}
}
