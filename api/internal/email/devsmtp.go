package email

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

// devSMTP stands in for SES in local dev and e2e (DEV_MODE + DEV_SMTP_ADDR):
// it turns the exact SendEmailInput the production path builds into a MIME
// message and hands it to an SMTP server such as Mailpit. LocalStack's free
// tier doesn't implement SES v2, so without this nothing is ever delivered
// locally.
type devSMTP struct {
	addr string
}

func (d devSMTP) SendEmail(_ context.Context, in *sesv2.SendEmailInput, _ ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
	if in.Content == nil || in.Content.Simple == nil || in.Content.Simple.Body == nil || in.Destination == nil {
		return nil, fmt.Errorf("dev smtp: only simple messages are supported")
	}
	msg := in.Content.Simple
	from := aws.ToString(in.FromEmailAddress)
	fromAddr, err := mail.ParseAddress(from)
	if err != nil {
		return nil, fmt.Errorf("dev smtp: from %q: %w", from, err)
	}
	id := randomID()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	h := textproto.MIMEHeader{}
	h.Set("From", from)
	h.Set("To", strings.Join(in.Destination.ToAddresses, ", "))
	if len(in.ReplyToAddresses) > 0 {
		h.Set("Reply-To", strings.Join(in.ReplyToAddresses, ", "))
	}
	h.Set("Subject", mime.QEncoding.Encode("utf-8", textOf(msg.Subject)))
	h.Set("Date", time.Now().UTC().Format(time.RFC1123Z))
	h.Set("Message-ID", "<"+id+"@dev.numun.local>")
	h.Set("MIME-Version", "1.0")
	h.Set("Content-Type", "multipart/alternative; boundary="+mw.Boundary())
	for _, mh := range msg.Headers {
		h.Set(aws.ToString(mh.Name), aws.ToString(mh.Value))
	}

	var out bytes.Buffer
	for k, vs := range h {
		for _, v := range vs {
			fmt.Fprintf(&out, "%s: %s\r\n", k, v)
		}
	}
	out.WriteString("\r\n")

	for _, part := range []struct {
		ctype string
		body  string
	}{
		{"text/plain", textOf(msg.Body.Text)},
		{"text/html", textOf(msg.Body.Html)},
	} {
		if part.body == "" {
			continue
		}
		pw, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.ctype + "; charset=utf-8"},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, err
		}
		qp := quotedprintable.NewWriter(pw)
		if _, err := qp.Write([]byte(part.body)); err != nil {
			return nil, err
		}
		if err := qp.Close(); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	out.Write(body.Bytes())

	if err := smtp.SendMail(d.addr, nil, fromAddr.Address, in.Destination.ToAddresses, out.Bytes()); err != nil {
		return nil, fmt.Errorf("dev smtp: %w", err)
	}
	return &sesv2.SendEmailOutput{MessageId: aws.String(id)}, nil
}

func textOf(c *sesv2types.Content) string {
	if c == nil {
		return ""
	}
	return aws.ToString(c.Data)
}

func randomID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
