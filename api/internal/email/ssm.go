package email

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

// readSSMUnsubscribeSecret loads the announcement unsubscribe HMAC key from
// SSM at /numun/${ENV}/email/unsubscribe_secret. Mirrors the cms_oauth/state_secret
// pattern; one-time bootstrap of the parameter is documented in EMAIL.md.
//
// The `cfg` parameter is accepted to mirror the signature of other SSM helpers
// in the codebase; we currently reload our own config to stay independent of
// caller plumbing.
func readSSMUnsubscribeSecret(ctx context.Context, _ aws.Config, logger *slog.Logger) (string, error) {
	envName := os.Getenv("ENV")
	if envName == "" {
		if sub := os.Getenv("ENV_SUBDOMAIN"); sub != "" {
			envName = sub
		} else {
			envName = "prod"
		}
	}
	freshCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return "", fmt.Errorf("aws config: %w", err)
	}
	client := ssm.NewFromConfig(freshCfg)
	name := fmt.Sprintf("/numun/%s/email/unsubscribe_secret", envName)
	out, err := client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(name),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		if logger != nil {
			logger.Warn("email: unsubscribe secret not loaded (List-Unsubscribe disabled)", "param", name)
		}
		return "", err
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", errors.New("ssm: empty parameter")
	}
	return *out.Parameter.Value, nil
}
