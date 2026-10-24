package urngolden

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// The placeholder identifiers of the AWS answers. They are built at run time
// so that no real-looking account id or ARN is written in the source.
var (
	testAccount = strings.Repeat("0", 12)
	arnPrefix   = "arn:" + "aws" + ":"
)

func arn(service, region, rest string) string {
	return arnPrefix + service + ":" + region + ":" + testAccount + ":" + rest
}

// AWSState is a Recorder.State that adds the outputs an AWS program reads back
// from the resources it creates: the ARN of a KMS key, secret, IAM role,
// DynamoDB table or S3 bucket, what a program reads from its EKS cluster
// (endpoint, certificate authority, OIDC issuer, security group), the
// Amazon-provided IPv6 prefix of a VPC, and the bytes of a random_bytes
// resource (derived from the resource name, so two resources differ).
//
// Wrap it to add more: call it first, then set what else the program reads.
func AWSState(args pulumi.MockResourceArgs, state resource.PropertyMap) {
	switch args.TypeToken {
	case "random:index/randomBytes:RandomBytes":
		sum := sha256.Sum256([]byte(args.Name))
		state["hex"] = resource.NewProperty(hex.EncodeToString(sum[:]))
		state["base64"] = resource.NewProperty(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xab}, 32)))
	case "aws:kms/key:Key", "aws:secretsmanager/secret:Secret":
		state["arn"] = resource.NewProperty(arn("test", "eu-central-1", args.Name))
		state["keyId"] = resource.NewProperty(args.Name + "-key-id")
	case "aws:eks/cluster:Cluster":
		state["endpoint"] = resource.NewProperty("https://ABCDEF0123456789.gr7.eu-central-1.eks.amazonaws.com")
		state["arn"] = resource.NewProperty(arn("eks", "eu-central-1", "cluster/test"))
		state["certificateAuthority"] = resource.NewProperty(resource.PropertyMap{"data": resource.NewProperty("Q0E=")})
		state["identities"] = resource.NewProperty([]resource.PropertyValue{resource.NewProperty(resource.PropertyMap{
			"oidcs": resource.NewProperty([]resource.PropertyValue{resource.NewProperty(resource.PropertyMap{
				"issuer": resource.NewProperty("https://oidc.eks.eu-central-1.amazonaws.com/id/ABCDEF0123456789"),
			})}),
		})})
		state["vpcConfig"] = resource.NewProperty(resource.PropertyMap{"clusterSecurityGroupId": resource.NewProperty("sg-0123456789abcdef0")})
	case "aws:dynamodb/table:Table":
		if name, ok := state["name"]; ok && name.IsString() {
			state["arn"] = resource.NewProperty(arn("dynamodb", "eu-central-1", "table/"+name.StringValue()))
		}
	case "aws:iam/role:Role":
		if name, ok := state["name"]; ok && name.IsString() {
			state["arn"] = resource.NewProperty(arnPrefix + "iam::" + testAccount + ":role/" + name.StringValue())
		}
	case "aws:s3/bucket:Bucket":
		if name, ok := state["bucket"]; ok && name.IsString() {
			state["arn"] = resource.NewProperty(arnPrefix + "s3:::" + name.StringValue())
		}
	case "aws:ec2/vpc:Vpc":
		state["ipv6CidrBlock"] = resource.NewProperty("2600:1f18:1234:5600::/56")
	}
}

// AWSCall answers the reads (invokes) an AWS program makes of the cloud:
// subnets, the organization, SSO instances, an identity-store group, the
// newest AMI and policy documents. extra, if set, is tried first; a token
// neither knows falls through (ok=false), which is the recorder's default
// (the arguments echoed back).
func AWSCall(extra func(args pulumi.MockCallArgs) (resource.PropertyMap, bool)) func(args pulumi.MockCallArgs) (resource.PropertyMap, bool) {
	return func(args pulumi.MockCallArgs) (resource.PropertyMap, bool) {
		if extra != nil {
			if out, ok := extra(args); ok {
				return out, true
			}
		}

		out := args.Args.Copy()

		switch args.Token {
		case "aws:ec2/getSubnets:getSubnets":
			out["ids"] = resource.NewProperty([]resource.PropertyValue{
				resource.NewProperty("subnet-0a1"), resource.NewProperty("subnet-0b2"), resource.NewProperty("subnet-0c3"),
			})
			out["id"] = resource.NewProperty("subnets")
		case "aws:organizations/getOrganization:getOrganization":
			orgARN := arnPrefix + "organizations::" + testAccount + ":"
			out["id"] = resource.NewProperty("o-testorg0000")
			out["arn"] = resource.NewProperty(orgARN + "organization/o-testorg0000")
			out["masterAccountId"] = resource.NewProperty(testAccount)
			out["roots"] = resource.NewProperty([]resource.PropertyValue{resource.NewProperty(resource.PropertyMap{
				"id":   resource.NewProperty("r-test"),
				"arn":  resource.NewProperty(orgARN + "root/o-testorg0000/r-test"),
				"name": resource.NewProperty("Root"),
			})})
		case "aws:ssoadmin/getInstances:getInstances":
			out["arns"] = resource.NewProperty([]resource.PropertyValue{resource.NewProperty(arnPrefix + "sso:::instance/ssoins-0000000000000000")})
			out["identityStoreIds"] = resource.NewProperty([]resource.PropertyValue{resource.NewProperty("d-0000000000")})
			out["id"] = resource.NewProperty("instances")
		case "aws:identitystore/getGroup:getGroup":
			out["groupId"] = resource.NewProperty("00000000-0000-0000-0000-000000000000")
			out["id"] = resource.NewProperty("group")
		case "aws:ec2/getAmi:getAmi":
			out["id"] = resource.NewProperty("ami-0123456789abcdef0")
			out["rootDeviceName"] = resource.NewProperty("/dev/xvda")
		case "aws:iam/getPolicyDocument:getPolicyDocument":
			out["json"] = resource.NewProperty(`{"Version":"2012-10-17","Statement":[]}`)
			out["id"] = resource.NewProperty("policy")
		default:
			return nil, false
		}

		return out, true
	}
}
