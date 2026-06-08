package handlers

// scope-check: skip
//
// Validation interceptor wiring. Runs protovalidate on every Connect request
// message; failures are surfaced as connect.CodeInvalidArgument with a
// google.rpc.BadRequest detail attached so the portal can render per-field
// errors.

import (
	"context"
	"errors"
	"strings"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"
)

// NewValidationInterceptor returns a Connect interceptor that validates the
// request body (when it implements proto.Message) using protovalidate. Any
// protovalidate.ValidationError is rewritten to invalid_argument + BadRequest.
func NewValidationInterceptor() (connect.UnaryInterceptorFunc, error) {
	v, err := protovalidate.New()
	if err != nil {
		return nil, err
	}
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return connect.UnaryFunc(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			msg, ok := req.Any().(proto.Message)
			if ok {
				if vErr := v.Validate(msg); vErr != nil {
					return nil, asInvalidArgument(vErr)
				}
			}
			return next(ctx, req)
		})
	}, nil
}

func asInvalidArgument(err error) error {
	var vErr *protovalidate.ValidationError
	if !errors.As(err, &vErr) {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	br := &errdetails.BadRequest{}
	for _, v := range vErr.Violations {
		path := pathString(v)
		desc := ""
		if v.Proto != nil {
			desc = v.Proto.GetMessage()
		}
		br.FieldViolations = append(br.FieldViolations, &errdetails.BadRequest_FieldViolation{
			Field:       path,
			Description: desc,
		})
	}
	cerr := connect.NewError(connect.CodeInvalidArgument, errors.New("validation failed"))
	if d, dErr := connect.NewErrorDetail(br); dErr == nil {
		cerr.AddDetail(d)
	}
	return cerr
}

func pathString(v *protovalidate.Violation) string {
	if v == nil || v.Proto == nil {
		return ""
	}
	parts := make([]string, 0, len(v.Proto.GetField().GetElements()))
	for _, el := range v.Proto.GetField().GetElements() {
		parts = append(parts, el.GetFieldName())
	}
	return strings.Join(parts, ".")
}
