#!/usr/bin/env bash
# LocalStack init hook — runs once when LocalStack signals ready.
# Creates the buckets, queues, and topics the API expects in the test env.
# Local-dev parity tracks the test (staging) environment names so the same
# DDB_TABLE_NAME and bucket-name env vars work locally and against AWS.

set -euo pipefail

awslocal s3 mb s3://numun-test-site      2>/dev/null || true
awslocal s3 mb s3://numun-test-portal    2>/dev/null || true
awslocal s3 mb s3://numun-test-cms       2>/dev/null || true
awslocal s3 mb s3://numun-test-assets    2>/dev/null || true
awslocal s3 mb s3://numun-test-uploads   2>/dev/null || true
awslocal s3 mb s3://numun-test-artifacts 2>/dev/null || true

# Email pipeline (EMAIL.md §3).
awslocal sqs create-queue --queue-name numun-test-email-send-dlq         >/dev/null 2>&1 || true
awslocal sqs create-queue --queue-name numun-test-email-send             >/dev/null 2>&1 || true
awslocal sns create-topic --name      numun-test-email-feedback          >/dev/null 2>&1 || true
awslocal sns create-topic --name      numun-test-alarms                  >/dev/null 2>&1 || true

# SES sender identities (verified automatically in LocalStack).
# Mail subdomain mirrors the test apex so the local env matches what M9 sets
# up for real staging email (`mail.test.numun.org`).
awslocal ses verify-email-identity --email-address noreply@mail.test.numun.org        >/dev/null 2>&1 || true
awslocal ses verify-email-identity --email-address announcements@mail.test.numun.org  >/dev/null 2>&1 || true
awslocal ses verify-email-identity --email-address cognito@mail.test.numun.org        >/dev/null 2>&1 || true

echo "LocalStack init complete."
