package main

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

// runLocal stands in for the SQS event source mapping in local dev and e2e:
// it long-polls EMAIL_QUEUE_URL (LocalStack), runs each batch through the
// same Handle the Lambda uses, and deletes the messages that succeeded so
// failures are redelivered like in AWS. Only allowed with DEV_MODE=true.
func runLocal(ctx context.Context, h *handler) error {
	if os.Getenv("DEV_MODE") != "true" {
		return errors.New("-local requires DEV_MODE=true")
	}
	queueURL := os.Getenv("EMAIL_QUEUE_URL")
	if queueURL == "" {
		return errors.New("-local requires EMAIL_QUEUE_URL")
	}
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return err
	}
	q := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		if ep := os.Getenv("AWS_ENDPOINT_URL_SQS"); ep != "" {
			o.BaseEndpoint = aws.String(ep)
		}
	})
	h.logger.Info("email-worker: polling", "queue", queueURL)
	for ctx.Err() == nil {
		out, err := q.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(queueURL),
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     2,
		})
		if err != nil {
			h.logger.Error("email-worker: receive", "err", err)
			time.Sleep(time.Second)
			continue
		}
		if len(out.Messages) == 0 {
			continue
		}
		evt := events.SQSEvent{}
		for _, m := range out.Messages {
			evt.Records = append(evt.Records, events.SQSMessage{
				MessageId: aws.ToString(m.MessageId),
				Body:      aws.ToString(m.Body),
			})
		}
		resp, _ := h.Handle(ctx, evt)
		failed := map[string]bool{}
		for _, f := range resp.BatchItemFailures {
			failed[f.ItemIdentifier] = true
		}
		for _, m := range out.Messages {
			if failed[aws.ToString(m.MessageId)] {
				continue
			}
			if _, err := q.DeleteMessage(ctx, &sqs.DeleteMessageInput{
				QueueUrl:      aws.String(queueURL),
				ReceiptHandle: m.ReceiptHandle,
			}); err != nil {
				h.logger.Warn("email-worker: delete", "err", err)
			}
		}
	}
	return ctx.Err()
}
