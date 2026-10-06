package urngolden

import (
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FakeAWS points the AWS SDK at a local server, so a program that asks the
// account a question (does this IAM role already exist, what pod identity
// associations does the cluster have) gets an answer with no network and no
// credentials. The answer is "nothing exists yet": the program then declares
// every resource without an import, which is the same set of names.
//
// The profile is written to a temporary shared config file so the program's
// own profile lookup (for example a per-account named profile) resolves.
func FakeAWS(t *testing.T, profile, region string) {
	t.Helper()

	FakeAWSProfiles(t, region, profile)
}

// FakeAWSProfiles is FakeAWS for a program that reads from several accounts
// (a stack with a hub and a spoke account). It also answers Route 53's
// ListHostedZonesByName with one private zone of the name asked for, which is
// how the cross-account zone associations look their zones up.
func FakeAWSProfiles(t *testing.T, region string, profiles ...string) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/clusters/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"associations":[]}`))

			return
		}

		if strings.HasSuffix(r.URL.Path, "/hostedzonesbyname") {
			name := html.EscapeString(r.URL.Query().Get("dnsname"))

			w.Header().Set("Content-Type", "text/xml")
			_, _ = w.Write([]byte(`<ListHostedZonesByNameResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
				`<HostedZones><HostedZone><Id>/hostedzone/Z0TESTZONE</Id><Name>` + name + `</Name>` +
				`<CallerReference>test</CallerReference><Config><PrivateZone>true</PrivateZone></Config>` +
				`<ResourceRecordSetCount>2</ResourceRecordSetCount></HostedZone></HostedZones>` +
				`<DNSName>` + name + `</DNSName><IsTruncated>false</IsTruncated><MaxItems>100</MaxItems>` +
				`</ListHostedZonesByNameResponse>`))

			return
		}

		if r.Method == http.MethodPost {
			// Load balancers: none exist yet (a program then skips
			// its endpoint record, as on a cluster ArgoCD has not synced).
			if body, _ := io.ReadAll(r.Body); strings.Contains(string(body), "Action=DescribeLoadBalancers") {
				w.Header().Set("Content-Type", "text/xml")
				_, _ = w.Write([]byte(`<DescribeLoadBalancersResponse xmlns="http://elasticloadbalancing.amazonaws.com/doc/2015-12-01/">` +
					`<DescribeLoadBalancersResult><LoadBalancers/></DescribeLoadBalancersResult>` +
					`<ResponseMetadata><RequestId>test</RequestId></ResponseMetadata></DescribeLoadBalancersResponse>`))

				return
			}
		}

		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<ErrorResponse xmlns="https://iam.amazonaws.com/doc/2010-05-08/"><Error>` +
			`<Type>Sender</Type><Code>NoSuchEntity</Code><Message>not found</Message></Error>` +
			`<RequestId>test</RequestId></ErrorResponse>`))
	}))
	t.Cleanup(server.Close)

	dir := t.TempDir()
	config := filepath.Join(dir, "config")

	var body string
	for _, profile := range profiles {
		body += "[profile " + profile + "]\nregion = " + region + "\n" +
			"aws_access_key_id = AKIATESTTESTTESTTEST\naws_secret_access_key = test\n\n"
	}
	if err := os.WriteFile(config, []byte(body), 0o600); err != nil {
		t.Fatalf("write aws config: %v", err)
	}

	t.Setenv("AWS_CONFIG_FILE", config)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "none"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_ENDPOINT_URL", server.URL)
	t.Setenv("AWS_RETRY_MAX_ATTEMPTS", "1")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
}
