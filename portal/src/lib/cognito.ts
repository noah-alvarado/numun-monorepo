// Browser-side Cognito wrapper for the portal sign-up / sign-in screens.
//
// The portal makes Cognito calls directly via `amazon-cognito-identity-js`
// (no Hosted UI) per AUTH.md §1. On success the tokens are POST'd to
// AuthService.Exchange, which mints the opaque `numun_session` cookie. The
// portal never holds tokens in JS beyond the brief Exchange call.
//
// In dev-bypass mode (VITE_DEV_BYPASS=true), the Cognito SDK is short-circuited
// — sign-in just calls Exchange with synthetic dev-* token values so the
// portal works against `make dev` with no real Cognito.

import { ConnectError } from "@connectrpc/connect";
import {
  CognitoUser,
  CognitoUserAttribute,
  CognitoUserPool,
  CognitoUserSession,
  AuthenticationDetails,
} from "amazon-cognito-identity-js";

import { connectMessage } from "@/lib/errors";

const userPoolId = import.meta.env.VITE_COGNITO_USER_POOL_ID as
  | string
  | undefined;
const clientId = import.meta.env.VITE_COGNITO_CLIENT_ID as string | undefined;
// Overrides the Cognito API URL; the e2e suite points it at a fake server.
const endpoint = import.meta.env.VITE_COGNITO_ENDPOINT as string | undefined;
export const devBypass = import.meta.env.VITE_DEV_BYPASS === "true";

const pool =
  !devBypass && userPoolId && clientId
    ? new CognitoUserPool({
        UserPoolId: userPoolId,
        ClientId: clientId,
        endpoint: endpoint || undefined,
      })
    : null;

export type AuthTokens = {
  idToken: string;
  accessToken: string;
  refreshToken: string;
  expiresIn: number;
};

function tokensFromSession(session: CognitoUserSession): AuthTokens {
  return {
    idToken: session.getIdToken().getJwtToken(),
    accessToken: session.getAccessToken().getJwtToken(),
    refreshToken: session.getRefreshToken().getToken(),
    expiresIn: Math.max(
      60,
      session.getAccessToken().getExpiration() - Math.floor(Date.now() / 1000),
    ),
  };
}

// signUp triggers Cognito.SignUp. The portal never surfaces a "user already
// exists" path — AUTH.md §3.1 mandates a uniform "check your email" response.
export async function signUp(input: {
  email: string;
  password: string;
  name: string;
  phone: string;
}): Promise<void> {
  if (!pool) {
    // Dev shortcut: no real Cognito. The seeded users already exist; pretend
    // we sent a verification email so the UX flow remains testable.
    return;
  }
  const attrs: CognitoUserAttribute[] = [
    new CognitoUserAttribute({ Name: "email", Value: input.email }),
    new CognitoUserAttribute({ Name: "name", Value: input.name }),
    new CognitoUserAttribute({ Name: "phone_number", Value: input.phone }),
  ];
  await new Promise<void>((resolve, reject) => {
    pool.signUp(input.email, input.password, attrs, [], (err) => {
      if (err) {
        // UsernameExistsException → swallow per AUTH.md §3.1.
        if ((err as { code?: string }).code === "UsernameExistsException") {
          resolve();
          return;
        }
        reject(err);
        return;
      }
      resolve();
    });
  });
}

export async function confirmSignUp(
  email: string,
  code: string,
): Promise<void> {
  if (!pool) return;
  const user = new CognitoUser({ Username: email, Pool: pool });
  await new Promise<void>((resolve, reject) => {
    user.confirmRegistration(code, true, (err) =>
      err ? reject(err) : resolve(),
    );
  });
}

export type SignInResult =
  | { kind: "success"; tokens: AuthTokens }
  | { kind: "new-password-required"; user: CognitoUser };

export async function signIn(
  email: string,
  password: string,
): Promise<SignInResult> {
  if (!pool) {
    // Dev bypass: synthesize tokens so AuthService.Exchange's dev branch
    // accepts them. The "email" doubles as the seed user id for now —
    // callers in dev pass the seed UUID directly.
    return {
      kind: "success",
      tokens: {
        idToken: "dev-token",
        accessToken: "dev-" + email,
        refreshToken: "dev-rt",
        expiresIn: 3600,
      },
    };
  }
  const user = new CognitoUser({ Username: email, Pool: pool });
  // The app client only allows USER_PASSWORD_AUTH (AUTH.md §4.2); the library defaults to SRP.
  user.setAuthenticationFlowType("USER_PASSWORD_AUTH");
  return new Promise<SignInResult>((resolve, reject) => {
    user.authenticateUser(
      new AuthenticationDetails({ Username: email, Password: password }),
      {
        onSuccess: (session) =>
          resolve({ kind: "success", tokens: tokensFromSession(session) }),
        onFailure: (err) => reject(err),
        newPasswordRequired: () =>
          resolve({ kind: "new-password-required", user }),
      },
    );
  });
}

export async function completeNewPasswordChallenge(
  user: CognitoUser,
  newPassword: string,
): Promise<AuthTokens> {
  return new Promise<AuthTokens>((resolve, reject) => {
    user.completeNewPasswordChallenge(
      newPassword,
      {},
      {
        onSuccess: (session) => resolve(tokensFromSession(session)),
        onFailure: (err) => reject(err),
      },
    );
  });
}

export async function forgotPassword(email: string): Promise<void> {
  if (!pool) return;
  const user = new CognitoUser({ Username: email, Pool: pool });
  await new Promise<void>((resolve, reject) => {
    user.forgotPassword({
      onSuccess: () => resolve(),
      onFailure: (err) => reject(err),
      inputVerificationCode: () => resolve(),
    });
  });
}

export async function confirmForgotPassword(
  email: string,
  code: string,
  newPassword: string,
): Promise<void> {
  if (!pool) return;
  const user = new CognitoUser({ Username: email, Pool: pool });
  await new Promise<void>((resolve, reject) => {
    user.confirmPassword(code, newPassword, {
      onSuccess: () => resolve(),
      onFailure: (err) => reject(err),
    });
  });
}

export async function resendSignUpCode(email: string): Promise<void> {
  if (!pool) return;
  const user = new CognitoUser({ Username: email, Pool: pool });
  await new Promise<void>((resolve, reject) => {
    user.resendConfirmationCode((err) => (err ? reject(err) : resolve()));
  });
}

export function cognitoErrorCode(err: unknown): string | undefined {
  return (err as { code?: string } | null)?.code;
}

// cognitoMessage turns a Cognito error into copy for the auth screens.
// Wrong-password and unknown-user errors share one message so the screens
// don't reveal which emails have accounts (AUTH.md §3.1).
export function cognitoMessage(err: unknown, fallback: string): string {
  // API errors (e.g. AuthService.Exchange being rate limited) aren't Cognito's.
  if (err instanceof ConnectError) return connectMessage(err) ?? fallback;
  const message = (err as Error | null)?.message ?? "";
  switch (cognitoErrorCode(err)) {
    case "NotAuthorizedException":
      if (/attempts exceeded/i.test(message)) return TOO_MANY;
      if (/disabled/i.test(message)) {
        return "This account is disabled. Contact NUMUN staff.";
      }
      return "Incorrect email or password.";
    case "UserNotFoundException":
      return "Incorrect email or password.";
    case "TooManyRequestsException":
    case "LimitExceededException":
    case "TooManyFailedAttemptsException":
      return TOO_MANY;
    case "InvalidPasswordException":
      return "Use at least 12 characters, including a lowercase letter and a number.";
    case "CodeMismatchException":
      return "That code doesn't match. Check the email we sent and try again.";
    case "ExpiredCodeException":
      return "That code has expired. Request a new one.";
    case "NetworkError":
      return "Couldn't reach the sign-in service. Check your connection and try again.";
  }
  return message || fallback;
}

const TOO_MANY = "Too many attempts. Wait a few minutes and try again.";
