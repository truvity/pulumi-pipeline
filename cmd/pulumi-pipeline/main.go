// Command pulumi-pipeline runs a DAG of Pulumi stacks: one scope per
// subdirectory of the scopes directory, one step per committed stack file
// (Pulumi.{stack}.yaml). Scopes and steps are discovered at startup.
//
//	pulumi-pipeline --scopes-dir deploy list             # scopes
//	pulumi-pipeline --scopes-dir deploy net list         # steps in a scope
//	pulumi-pipeline --scopes-dir deploy net diff --all   # drift check
//	pulumi-pipeline --scopes-dir deploy net deploy app
package main

import (
	"context"
	"os"

	"github.com/truvity/pulumi-pipeline/pkg/pulumicli"
)

func main() {
	os.Exit(pulumicli.Main(context.Background(), os.Args, pulumicli.Options{}))
}
