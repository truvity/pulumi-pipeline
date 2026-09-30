// Command program is the smallest Pulumi program the pulumistep tests can run
// against a local file backend: it registers `count` component resources, which
// the engine tracks itself, so no provider plugin and no cloud is involved.
package main

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		n := config.New(ctx, "").GetInt("count")

		for i := range n {
			var component pulumi.ResourceState
			if err := ctx.RegisterComponentResource("test:index:Thing", fmt.Sprintf("thing-%d", i), &component); err != nil {
				return err
			}
		}

		ctx.Export("count", pulumi.Int(n))
		ctx.Export("token", pulumi.ToSecret(pulumi.String("not-for-disk")))

		return nil
	})
}
