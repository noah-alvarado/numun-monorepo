#!/usr/bin/env bash
# Generalized end-to-end auth verification against any deployed env.
#
# Inputs (all required; pass via env vars to keep secrets out of `ps`):
#
#   API_BASE_URL       e.g. https://6dx8ilgqp3.execute-api.us-east-2.amazonaws.com
#                      or https://api.test.numun.org once DNS is cut over
#   COGNITO_USER_POOL  e.g. us-east-2_mFmtWvKtQ
#   COGNITO_CLIENT_ID  e.g. 7j6o7ub2h7tl4ln21tfqljobun
#   AWS_PROFILE        e.g. numun-prod (or whichever profile reaches the pool)
#   AWS_REGION         e.g. us-east-2
#   ADMIN_EMAIL        the bootstrapped admin's email
#
# Prompts for the temp password (if the user is still in FORCE_CHANGE_PASSWORD)
# OR an existing password (otherwise), plus — when needed — a new password for
# the NEW_PASSWORD_REQUIRED challenge. Passwords are read via `read -s`.
#
# Exercises: InitiateAuth → optional RespondToAuthChallenge →
# AuthService.Exchange → UserService.GetMe → AuthService.Logout →
# re-GetMe-expect-401. Tokens never written to disk or stdout.

set -euo pipefail

: "${API_BASE_URL:?set API_BASE_URL}"
: "${COGNITO_USER_POOL:?set COGNITO_USER_POOL}"
: "${COGNITO_CLIENT_ID:?set COGNITO_CLIENT_ID}"
: "${AWS_PROFILE:?set AWS_PROFILE}"
: "${AWS_REGION:?set AWS_REGION}"
: "${ADMIN_EMAIL:?set ADMIN_EMAIL}"

if ! command -v jq >/dev/null; then
  echo "this script needs jq"
  exit 1
fi

read -rsp "Password for ${ADMIN_EMAIL}: " PW; echo

echo
echo "=== Step 1: InitiateAuth (USER_PASSWORD_AUTH) ==="
INIT=$(aws cognito-idp initiate-auth \
  --profile "$AWS_PROFILE" --region "$AWS_REGION" \
  --client-id "$COGNITO_CLIENT_ID" \
  --auth-flow USER_PASSWORD_AUTH \
  --auth-parameters USERNAME="$ADMIN_EMAIL",PASSWORD="$PW")
CHALLENGE=$(echo "$INIT" | jq -r '.ChallengeName // empty')

if [[ "$CHALLENGE" == "NEW_PASSWORD_REQUIRED" ]]; then
  SESSION=$(echo "$INIT" | jq -r '.Session')
  echo "got NEW_PASSWORD_REQUIRED challenge — setting permanent password"
  read -rsp "New password (12+ chars, lowercase + digit): " NEW_PW; echo
  read -rsp "Confirm new password: " NEW_PW2; echo
  if [[ "$NEW_PW" != "$NEW_PW2" ]]; then
    echo "passwords don't match"; exit 1
  fi
  echo
  echo "=== Step 2: RespondToAuthChallenge ==="
  RESP=$(aws cognito-idp respond-to-auth-challenge \
    --profile "$AWS_PROFILE" --region "$AWS_REGION" \
    --client-id "$COGNITO_CLIENT_ID" \
    --challenge-name NEW_PASSWORD_REQUIRED \
    --session "$SESSION" \
    --challenge-responses USERNAME="$ADMIN_EMAIL",NEW_PASSWORD="$NEW_PW")
  unset NEW_PW NEW_PW2
elif [[ -n "$CHALLENGE" ]]; then
  echo "unexpected challenge: $CHALLENGE"
  echo "$INIT" | jq 'del(.AuthenticationResult)'
  exit 1
else
  echo "no challenge — using returned tokens directly"
  RESP="$INIT"
fi
unset PW

ID_TOKEN=$(echo "$RESP" | jq -r '.AuthenticationResult.IdToken')
ACCESS_TOKEN=$(echo "$RESP" | jq -r '.AuthenticationResult.AccessToken')
REFRESH_TOKEN=$(echo "$RESP" | jq -r '.AuthenticationResult.RefreshToken')
EXPIRES_IN=$(echo "$RESP" | jq -r '.AuthenticationResult.ExpiresIn')
if [[ -z "$ID_TOKEN" || "$ID_TOKEN" == "null" ]]; then
  echo "no tokens returned"
  echo "$RESP" | jq .
  exit 1
fi

echo
echo "=== Step 3: AuthService.Exchange ==="
EXCHANGE_BODY=$(jq -nc \
  --arg id "$ID_TOKEN" \
  --arg at "$ACCESS_TOKEN" \
  --arg rt "$REFRESH_TOKEN" \
  --argjson exp "$EXPIRES_IN" \
  '{idToken:$id, accessToken:$at, refreshToken:$rt, expiresIn:$exp, rememberMe:false}')

EX_HEADERS=$(mktemp)
EX_BODY=$(curl -sS -D "$EX_HEADERS" -o /dev/stdout \
  -X POST "$API_BASE_URL/numun.v1.AuthService/Exchange" \
  -H "Content-Type: application/json" \
  -H "Connect-Protocol-Version: 1" \
  -d "$EXCHANGE_BODY")
EX_STATUS=$(awk 'NR==1{print $2}' "$EX_HEADERS")
echo "Exchange status: $EX_STATUS"
if [[ "$EX_STATUS" != "200" ]]; then
  echo "body: $EX_BODY"
  rm "$EX_HEADERS"
  exit 1
fi

SESS_COOKIE=$(grep -i '^set-cookie: numun_session=' "$EX_HEADERS" | sed -E 's/.*numun_session=([^;]+).*/\1/' | tr -d '\r')
CSRF_COOKIE=$(grep -i '^set-cookie: csrf_token='    "$EX_HEADERS" | sed -E 's/.*csrf_token=([^;]+).*/\1/'    | tr -d '\r')
rm "$EX_HEADERS"
if [[ -z "$SESS_COOKIE" || -z "$CSRF_COOKIE" ]]; then
  echo "missing cookies in Exchange response"; exit 1
fi
echo "session + csrf cookies received"

echo
echo "=== Step 4: UserService.GetMe ==="
ME=$(curl -sS -X POST "$API_BASE_URL/numun.v1.UserService/GetMe" \
  -H "Content-Type: application/json" \
  -H "Connect-Protocol-Version: 1" \
  -H "Cookie: numun_session=$SESS_COOKIE; csrf_token=$CSRF_COOKIE" \
  -d '{}')
echo "$ME" | jq .

echo
echo "=== Step 5: AuthService.Logout ==="
LOGOUT_STATUS=$(curl -sS -o /dev/null -w '%{http_code}' \
  -X POST "$API_BASE_URL/numun.v1.AuthService/Logout" \
  -H "Content-Type: application/json" \
  -H "Connect-Protocol-Version: 1" \
  -H "X-CSRF-Token: $CSRF_COOKIE" \
  -H "Cookie: numun_session=$SESS_COOKIE; csrf_token=$CSRF_COOKIE" \
  -d '{}')
echo "Logout status: $LOGOUT_STATUS"

echo
echo "=== Step 6: GetMe after Logout (expect 401) ==="
POST_STATUS=$(curl -sS -o /dev/null -w '%{http_code}' \
  -X POST "$API_BASE_URL/numun.v1.UserService/GetMe" \
  -H "Content-Type: application/json" \
  -H "Connect-Protocol-Version: 1" \
  -H "Cookie: numun_session=$SESS_COOKIE; csrf_token=$CSRF_COOKIE" \
  -d '{}')
echo "Post-logout GetMe status: $POST_STATUS"

unset ID_TOKEN ACCESS_TOKEN REFRESH_TOKEN SESS_COOKIE CSRF_COOKIE
echo
echo "done"
