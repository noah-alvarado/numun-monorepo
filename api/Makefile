# SAM build hooks for the Lambdalith.
#
# `sam build` invokes the `build-<LogicalId>:` target with $(ARTIFACTS_DIR)
# set to a per-function staging dir. The target must place the runtime
# binary (named `bootstrap` for provided.al2023) into $(ARTIFACTS_DIR).

COMMIT_SHA      ?= dev
RELEASE_VERSION ?= 0.0.0

LDFLAGS := -s -w \
  -X main.commit=$(COMMIT_SHA) \
  -X main.version=$(RELEASE_VERSION)

build-ApiFunction:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
	  go build -trimpath -ldflags="$(LDFLAGS)" -o $(ARTIFACTS_DIR)/bootstrap ./cmd/api

build-CognitoPostConfirmationFunction:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
	  go build -trimpath -ldflags="$(LDFLAGS)" -o $(ARTIFACTS_DIR)/bootstrap ./cmd/cognito-post-confirmation

build-EmailWorkerFunction:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
	  go build -trimpath -ldflags="$(LDFLAGS)" -o $(ARTIFACTS_DIR)/bootstrap ./cmd/email-worker

build-EmailFeedbackFunction:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
	  go build -trimpath -ldflags="$(LDFLAGS)" -o $(ARTIFACTS_DIR)/bootstrap ./cmd/email-feedback
