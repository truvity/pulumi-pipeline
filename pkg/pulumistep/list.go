package pulumistep

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/truvity/pulumi-pipeline/pkg/pipeline"
)

const (
	dashStr = "—"
)

// ListMetadata returns a ListMetadataFunc that reads Pulumi.yaml project names
// and Pulumi.<stack>.yaml backend info for display in ListSteps.
func ListMetadata() pipeline.ListMetadataFunc {
	return func(repoRoot string, step *pipeline.Step) (project, stack, backend string) {
		if step.Kind != pipeline.StepPulumi {
			return dashStr, dashStr, dashStr
		}

		project = readPulumiProjectName(repoRoot, step.WorkDir)
		stack = step.StackName
		backend = readPulumiBackend(repoRoot, step.WorkDir, step.StackName)

		return project, stack, backend
	}
}

// readPulumiProjectName reads the "name" field from Pulumi.yaml in the given workDir.
func readPulumiProjectName(repoRoot, workDir string) string {
	data, err := os.ReadFile(filepath.Join(repoRoot, workDir, "Pulumi.yaml"))
	if err != nil {
		return "?"
	}

	var proj struct {
		Name string `yaml:"name"`
	}

	if err := yaml.Unmarshal(data, &proj); err != nil {
		return "?"
	}

	if proj.Name == "" {
		return "?"
	}

	return proj.Name
}

// readPulumiBackend reads the secrets provider from Pulumi.<stack>.yaml
// and extracts the profile and region (e.g., "ci@eu-example-1").
func readPulumiBackend(repoRoot, workDir, stackName string) string {
	path := filepath.Join(repoRoot, workDir, fmt.Sprintf("Pulumi.%s.yaml", stackName))

	data, err := os.ReadFile(path)
	if err != nil {
		return "?"
	}

	var stackCfg struct {
		SecretsProvider string `yaml:"secretsprovider"`
	}

	if err := yaml.Unmarshal(data, &stackCfg); err != nil {
		return "?"
	}

	return parseBackendInfo(stackCfg.SecretsProvider)
}

// parseBackendInfo extracts "profile@region" from a secrets provider URL like
// "awskms://alias/example-state?awssdk=v2&region=eu-example-1&profile=ci@admin".
func parseBackendInfo(secretsProvider string) string {
	if secretsProvider == "" {
		return "?"
	}

	u, err := url.Parse(secretsProvider)
	if err != nil {
		return "?"
	}

	region := u.Query().Get("region")
	profile := u.Query().Get("profile")

	if profile == "" || region == "" {
		return "?"
	}

	// Extract account name from profile (e.g., "ci@admin" → "ci").
	account := profile
	if idx := strings.Index(profile, "@"); idx > 0 {
		account = profile[:idx]
	}

	return account + "@" + region
}
