// Package ses sends email with Amazon SES (API v2).
package ses

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"

	"github.com/CodyLionVivo/duradent/services/api/internal/notifications/domain"
)

type Mailer struct {
	client *sesv2.Client
	from   string
	// configSet routes bounces and complaints to CloudWatch (optional).
	configSet string
}

func New(cfg aws.Config, from, configSet string) *Mailer {
	return &Mailer{client: sesv2.NewFromConfig(cfg), from: from, configSet: configSet}
}

func (m *Mailer) Send(ctx context.Context, msg domain.Message) (string, error) {
	utf8 := aws.String("UTF-8")
	in := &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(m.from),
		Destination:      &types.Destination{ToAddresses: []string{msg.To}},
		Content: &types.EmailContent{Simple: &types.Message{
			Subject: &types.Content{Data: aws.String(msg.Subject), Charset: utf8},
			Body: &types.Body{
				Html: &types.Content{Data: aws.String(msg.HTML), Charset: utf8},
				Text: &types.Content{Data: aws.String(msg.Text), Charset: utf8},
			},
		}},
	}
	if m.configSet != "" {
		in.ConfigurationSetName = aws.String(m.configSet)
	}
	out, err := m.client.SendEmail(ctx, in)
	if err != nil {
		return "", err
	}
	return aws.ToString(out.MessageId), nil
}
