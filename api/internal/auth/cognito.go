package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	cogtypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
)

// Cognito is a small wrapper around the Cognito Identity Provider client that
// captures the user-pool / client-id configuration the API needs.
//
// Configuration is taken from environment variables (set in prod via SAM
// template Variables, locally via scripts/sam-env-vars.json or .env.local):
//
//	COGNITO_USER_POOL_ID
//	COGNITO_CLIENT_ID
//
// In dev-bypass mode (DEV_BYPASS_AUTH=true + DEV_MODE=true), the wrapper is
// still constructed but its methods are not called.
type Cognito struct {
	Client     *cognitoidentityprovider.Client
	UserPoolID string
	ClientID   string
	Region     string
}

// NewCognito builds a Cognito wrapper from the ambient AWS config. Returns an
// error if the required env vars are missing AND dev-bypass is NOT enabled —
// the local-dev path doesn't need a real pool.
func NewCognito(ctx context.Context) (*Cognito, error) {
	userPool := os.Getenv("COGNITO_USER_POOL_ID")
	clientID := os.Getenv("COGNITO_CLIENT_ID")
	devBypass := os.Getenv("DEV_BYPASS_AUTH") == "true" && os.Getenv("DEV_MODE") == "true"

	if !devBypass && (userPool == "" || clientID == "") {
		return nil, errors.New("auth: COGNITO_USER_POOL_ID and COGNITO_CLIENT_ID required outside dev-bypass mode")
	}
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	c := cognitoidentityprovider.NewFromConfig(cfg)
	region := cfg.Region
	if region == "" {
		region = os.Getenv("AWS_REGION")
	}
	return &Cognito{
		Client:     c,
		UserPoolID: userPool,
		ClientID:   clientID,
		Region:     region,
	}, nil
}

// RefreshAccessToken calls Cognito InitiateAuth with REFRESH_TOKEN_AUTH and
// returns a fresh access token + its expires-in seconds. Used by the middleware
// when the cached access token has expired.
func (c *Cognito) RefreshAccessToken(ctx context.Context, refreshToken string) (accessToken string, expiresIn int32, err error) {
	out, err := c.Client.InitiateAuth(ctx, &cognitoidentityprovider.InitiateAuthInput{
		AuthFlow: cogtypes.AuthFlowTypeRefreshTokenAuth,
		ClientId: aws.String(c.ClientID),
		AuthParameters: map[string]string{
			"REFRESH_TOKEN": refreshToken,
		},
	})
	if err != nil {
		return "", 0, fmt.Errorf("refresh token: %w", err)
	}
	if out.AuthenticationResult == nil || out.AuthenticationResult.AccessToken == nil {
		return "", 0, errors.New("refresh token: no access token in response")
	}
	return *out.AuthenticationResult.AccessToken, out.AuthenticationResult.ExpiresIn, nil
}

// RevokeRefreshToken calls Cognito RevokeToken. Best-effort: logout proceeds
// regardless of the outcome. The endpoint requires the client id; no app
// client secret is configured for the portal client.
func (c *Cognito) RevokeRefreshToken(ctx context.Context, refreshToken string) error {
	_, err := c.Client.RevokeToken(ctx, &cognitoidentityprovider.RevokeTokenInput{
		Token:    aws.String(refreshToken),
		ClientId: aws.String(c.ClientID),
	})
	if err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	return nil
}

// AdminCreateUser invites a staff user. See AUTH.md §3.2.
type AdminCreateUserInput struct {
	Email string
	Name  string
	// Role must be "staff-admin" or "staff-staffer".
	Role string
}

// AdminCreateUserOutput carries the Cognito sub of the newly created user
// (which becomes the User row's id).
type AdminCreateUserOutput struct {
	Sub string
}

// AdminCreateUser invokes Cognito AdminCreateUser with DesiredDeliveryMediums:
// ["EMAIL"], setting custom:role and the optional name attribute. Returns the
// Cognito sub so the caller can write the mirror DDB User row.
func (c *Cognito) AdminCreateUser(ctx context.Context, in AdminCreateUserInput) (AdminCreateUserOutput, error) {
	if in.Role != "staff-admin" && in.Role != "staff-staffer" {
		return AdminCreateUserOutput{}, fmt.Errorf("invalid staff role: %q", in.Role)
	}
	attrs := []cogtypes.AttributeType{
		{Name: aws.String("email"), Value: aws.String(in.Email)},
		{Name: aws.String("email_verified"), Value: aws.String("true")},
		{Name: aws.String("custom:role"), Value: aws.String(in.Role)},
	}
	if in.Name != "" {
		attrs = append(attrs, cogtypes.AttributeType{Name: aws.String("name"), Value: aws.String(in.Name)})
	}
	out, err := c.Client.AdminCreateUser(ctx, &cognitoidentityprovider.AdminCreateUserInput{
		UserPoolId:             aws.String(c.UserPoolID),
		Username:               aws.String(in.Email),
		UserAttributes:         attrs,
		DesiredDeliveryMediums: []cogtypes.DeliveryMediumType{cogtypes.DeliveryMediumTypeEmail},
	})
	if err != nil {
		return AdminCreateUserOutput{}, fmt.Errorf("admin create user: %w", err)
	}
	if out.User == nil {
		return AdminCreateUserOutput{}, errors.New("admin create user: empty user in response")
	}
	for _, a := range out.User.Attributes {
		if a.Name != nil && *a.Name == "sub" && a.Value != nil {
			return AdminCreateUserOutput{Sub: *a.Value}, nil
		}
	}
	return AdminCreateUserOutput{}, errors.New("admin create user: sub attribute missing")
}

// LookupUserByEmail returns the Cognito sub for a given email, or "" if no
// user exists. Used by RecordPasswordResetCompleted to resolve the subject
// before writing the audit event.
func (c *Cognito) LookupUserByEmail(ctx context.Context, email string) (string, error) {
	out, err := c.Client.AdminGetUser(ctx, &cognitoidentityprovider.AdminGetUserInput{
		UserPoolId: aws.String(c.UserPoolID),
		Username:   aws.String(strings.ToLower(email)),
	})
	if err != nil {
		var notFound *cogtypes.UserNotFoundException
		if errors.As(err, &notFound) {
			return "", nil
		}
		return "", fmt.Errorf("admin get user: %w", err)
	}
	for _, a := range out.UserAttributes {
		if a.Name != nil && *a.Name == "sub" && a.Value != nil {
			return *a.Value, nil
		}
	}
	return "", nil
}
