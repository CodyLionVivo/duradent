// Package smtp sends email through a plain SMTP server. Locally that is
// Mailpit (http://localhost:8025 shows every message); AWS uses SES instead.
package smtp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/notifications/domain"
)

type Mailer struct {
	addr string
	from string
}

func New(addr, from string) *Mailer { return &Mailer{addr: addr, from: from} }

func (m *Mailer) Send(_ context.Context, msg domain.Message) (string, error) {
	if strings.ContainsAny(msg.To, "\r\n") {
		return "", fmt.Errorf("invalid recipient")
	}
	raw, msgID, err := build(m.from, msg)
	if err != nil {
		return "", err
	}
	return msgID, smtp.SendMail(m.addr, nil, envelope(m.from), []string{msg.To}, raw)
}

// envelope extracts the address from "Name <addr>".
func envelope(from string) string {
	if i := strings.LastIndex(from, "<"); i >= 0 {
		return strings.TrimSuffix(from[i+1:], ">")
	}
	return from
}

func build(from string, msg domain.Message) ([]byte, string, error) {
	var nonce [12]byte
	_, _ = rand.Read(nonce[:])
	msgID := fmt.Sprintf("<%s@duradent>", hex.EncodeToString(nonce[:]))

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, part := range []struct{ ctype, content string }{
		{"text/plain; charset=utf-8", msg.Text},
		{"text/html; charset=utf-8", msg.HTML},
	} {
		w, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type": {part.ctype}, "Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, "", err
		}
		qp := quotedprintable.NewWriter(w)
		if _, err := qp.Write([]byte(part.content)); err != nil {
			return nil, "", err
		}
		if err := qp.Close(); err != nil {
			return nil, "", err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}

	var out bytes.Buffer
	fmt.Fprintf(&out, "From: %s\r\n", from)
	fmt.Fprintf(&out, "To: %s\r\n", msg.To)
	fmt.Fprintf(&out, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", msg.Subject))
	fmt.Fprintf(&out, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&out, "Message-ID: %s\r\n", msgID)
	fmt.Fprintf(&out, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&out, "Content-Type: multipart/alternative; boundary=%s\r\n\r\n", mw.Boundary())
	out.Write(body.Bytes())
	return out.Bytes(), msgID, nil
}
