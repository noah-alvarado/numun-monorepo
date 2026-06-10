package cms

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// LoadConfigFromSSM resolves the GitHub App config from
// /numun/${ENV}/github_app/* parameters. Returns the loaded Config and nil
// on success. When any required parameter is missing (typical for `make dev`
// without GitHub App credentials), the returned error wraps ErrConfigMissing
// and callers should fall back to NewStub.
func LoadConfigFromSSM(ctx context.Context, logger *slog.Logger) (Config, error) {
	envName := os.Getenv("ENV")
	if envName == "" {
		if sub := os.Getenv("ENV_SUBDOMAIN"); sub != "" {
			envName = sub
		} else {
			envName = "prod"
		}
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return Config{}, fmt.Errorf("aws config: %w", err)
	}
	client := ssm.NewFromConfig(cfg)
	prefix := fmt.Sprintf("/numun/%s/github_app/", envName)
	read := func(suffix string, decrypt bool) (string, error) {
		name := prefix + suffix
		out, err := client.GetParameter(ctx, &ssm.GetParameterInput{
			Name:           aws.String(name),
			WithDecryption: aws.Bool(decrypt),
		})
		if err != nil {
			return "", err
		}
		if out.Parameter == nil || out.Parameter.Value == nil {
			return "", errors.New("ssm: empty parameter")
		}
		return *out.Parameter.Value, nil
	}
	out := Config{}
	missing := false
	for _, spec := range []struct {
		suffix  string
		decrypt bool
		dest    *string
	}{
		{"app_id", false, &out.AppID},
		{"installation_id", false, &out.InstallationID},
		{"private_key", true, &out.PrivateKeyPEM},
		{"repo", false, &out.Repo},
	} {
		v, err := read(spec.suffix, spec.decrypt)
		if err != nil {
			if logger != nil {
				logger.Warn("cms: SSM param missing — falling back to stub", "suffix", spec.suffix, "err", err)
			}
			missing = true
			break
		}
		*spec.dest = v
	}
	// Optional branch; defaults to "main" in New().
	if branch, err := read("branch", false); err == nil {
		out.Branch = branch
	}
	if missing {
		return out, ErrConfigMissing
	}
	return out, nil
}

// ErrConfigMissing signals that SSM didn't yield a complete GitHub App config.
// Callers should fall back to NewStub for local development.
var ErrConfigMissing = errors.New("cms: github app config missing in SSM")
