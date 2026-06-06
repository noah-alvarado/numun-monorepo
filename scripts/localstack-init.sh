#!/usr/bin/env bash
# LocalStack init hook — runs once when LocalStack signals ready.
# Creates the buckets, queues, and topics the API expects in prod.

set -euo pipefail

awslocal s3 mb s3://numun-org-site      2>/dev/null || true
awslocal s3 mb s3://numun-org-portal    2>/dev/null || true
awslocal s3 mb s3://numun-org-cms       2>/dev/null || true
awslocal s3 mb s3://numun-org-assets    2>/dev/null || true
awslocal s3 mb s3://numun-org-uploads   2>/dev/null || true
awslocal s3 mb s3://numun-org-artifacts 2>/dev/null || true

# Email pipeline (EMAIL.md §3).
awslocal sqs create-queue --queue-name numun-prod-email-send-dlq         >/dev/null 2>&1 || true
awslocal sqs create-queue --queue-name numun-prod-email-send             >/dev/null 2>&1 || true
awslocal sns create-topic --name      numun-prod-email-feedback          >/dev/null 2>&1 || true
awslocal sns create-topic --name      numun-prod-alarms                  >/dev/null 2>&1 || true

# SES sender identities (verified automatically in LocalStack).
awslocal ses verify-email-identity --email-address noreply@mail.numun.org        >/dev/null 2>&1 || true
awslocal ses verify-email-identity --email-address announcements@mail.numun.org  >/dev/null 2>&1 || true
awslocal ses verify-email-identity --email-address cognito@mail.numun.org        >/dev/null 2>&1 || true

echo "LocalStack init complete."
