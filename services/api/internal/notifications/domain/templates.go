// Package domain renders the transactional emails the store sends, in the
// customer's language (es, en, pt-br).
package domain

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
)

type Kind string

const (
	KindWelcome           Kind = "welcome"
	KindVerifyEmail       Kind = "verify_email"
	KindOrderPlaced       Kind = "order_placed"
	KindPaymentConfirmed  Kind = "payment_confirmed"
	KindOrderShipped      Kind = "order_shipped"
	KindOrderCancelled    Kind = "order_cancelled"
	KindOrderRefunded     Kind = "order_refunded"
	KindInvoiceIssued     Kind = "invoice_issued"
	KindAdminStockLow     Kind = "admin_stock_low"
	KindAdminInvoiceError Kind = "admin_invoice_error"
)

// Message is a rendered email.
type Message struct {
	To      string
	Subject string
	HTML    string
	Text    string
}

// Data fills the templates. Fields not used by a kind stay empty.
type Data struct {
	Name         string
	OrderNumber  string
	Total        string // formatted, e.g. "S/ 146.32"
	Method       string // culqi | bank_transfer
	BankDetails  string
	Deadline     string
	Carrier      string
	Tracking     string
	Reason       string
	DocumentName string // "Factura F001-123"
	PDFURL       string
	Link         string // order page or verification link
	LogoURL      string // absolute URL of the PNG logo (email clients do not show SVG)
	SKU          string
	Available    int
}

type copyText struct {
	Subject string
	Title   string
	Body    []string // paragraphs; may contain {{.Field}} placeholders
	Button  string
}

// texts holds every email in every language. Spanish is the reference.
var texts = map[Kind]map[locale.Locale]copyText{
	KindWelcome: {
		locale.ES:   {"Bienvenido a Duradent", "¡Hola, {{.Name}}!", []string{"Tu cuenta en Duradent está lista. Completa tus datos de facturación para comprar con factura y recibir los precios de tu clínica."}, "Ir a mi cuenta"},
		locale.EN:   {"Welcome to Duradent", "Hello, {{.Name}}!", []string{"Your Duradent account is ready. Complete your billing details to buy with an invoice and get your clinic's prices."}, "Go to my account"},
		locale.PTBR: {"Bem-vindo à Duradent", "Olá, {{.Name}}!", []string{"Sua conta na Duradent está pronta. Complete seus dados de faturamento para comprar com fatura e receber os preços da sua clínica."}, "Ir para minha conta"},
	},
	KindVerifyEmail: {
		locale.ES:   {"Confirma tu correo", "Confirma tu correo", []string{"Hola, {{.Name}}. Confirma que este correo es tuyo para proteger tu cuenta. El enlace vence en 24 horas.", "Si no creaste una cuenta en Duradent, ignora este mensaje."}, "Confirmar correo"},
		locale.EN:   {"Confirm your email", "Confirm your email", []string{"Hello, {{.Name}}. Confirm this email is yours to protect your account. The link expires in 24 hours.", "If you did not create a Duradent account, ignore this message."}, "Confirm email"},
		locale.PTBR: {"Confirme seu e-mail", "Confirme seu e-mail", []string{"Olá, {{.Name}}. Confirme que este e-mail é seu para proteger sua conta. O link vence em 24 horas.", "Se você não criou uma conta na Duradent, ignore esta mensagem."}, "Confirmar e-mail"},
	},
	KindOrderPlaced: {
		locale.ES:   {"Recibimos tu pedido {{.OrderNumber}}", "Pedido {{.OrderNumber}} recibido", []string{"Gracias por tu compra, {{.Name}}. Total: {{.Total}}.", "{{if eq .Method \"bank_transfer\"}}Transfiere el total indicando el número de pedido antes del {{.Deadline}}:<br>{{.BankDetails}}{{else}}Si aún no completaste el pago, hazlo antes del {{.Deadline}}.{{end}}"}, "Ver pedido"},
		locale.EN:   {"We received your order {{.OrderNumber}}", "Order {{.OrderNumber}} received", []string{"Thank you for your order, {{.Name}}. Total: {{.Total}}.", "{{if eq .Method \"bank_transfer\"}}Transfer the total quoting the order number before {{.Deadline}}:<br>{{.BankDetails}}{{else}}If you have not paid yet, please do so before {{.Deadline}}.{{end}}"}, "View order"},
		locale.PTBR: {"Recebemos seu pedido {{.OrderNumber}}", "Pedido {{.OrderNumber}} recebido", []string{"Obrigado pela compra, {{.Name}}. Total: {{.Total}}.", "{{if eq .Method \"bank_transfer\"}}Transfira o total informando o número do pedido antes de {{.Deadline}}:<br>{{.BankDetails}}{{else}}Se ainda não pagou, faça isso antes de {{.Deadline}}.{{end}}"}, "Ver pedido"},
	},
	KindPaymentConfirmed: {
		locale.ES:   {"Pago confirmado · pedido {{.OrderNumber}}", "¡Pago confirmado!", []string{"Recibimos {{.Total}} por tu pedido {{.OrderNumber}}. Ya lo estamos preparando; te avisaremos cuando salga."}, "Ver pedido"},
		locale.EN:   {"Payment confirmed · order {{.OrderNumber}}", "Payment confirmed!", []string{"We received {{.Total}} for your order {{.OrderNumber}}. We are preparing it and will let you know when it ships."}, "View order"},
		locale.PTBR: {"Pagamento confirmado · pedido {{.OrderNumber}}", "Pagamento confirmado!", []string{"Recebemos {{.Total}} pelo seu pedido {{.OrderNumber}}. Já estamos preparando e avisaremos quando sair."}, "Ver pedido"},
	},
	KindOrderShipped: {
		locale.ES:   {"Tu pedido {{.OrderNumber}} está en camino", "Pedido enviado", []string{"Tu pedido {{.OrderNumber}} salió de nuestro almacén.{{if .Carrier}} Transportista: {{.Carrier}}.{{end}}{{if .Tracking}} Guía: {{.Tracking}}.{{end}}"}, "Ver pedido"},
		locale.EN:   {"Your order {{.OrderNumber}} is on its way", "Order shipped", []string{"Your order {{.OrderNumber}} has left our warehouse.{{if .Carrier}} Carrier: {{.Carrier}}.{{end}}{{if .Tracking}} Tracking: {{.Tracking}}.{{end}}"}, "View order"},
		locale.PTBR: {"Seu pedido {{.OrderNumber}} está a caminho", "Pedido enviado", []string{"Seu pedido {{.OrderNumber}} saiu do nosso depósito.{{if .Carrier}} Transportadora: {{.Carrier}}.{{end}}{{if .Tracking}} Rastreio: {{.Tracking}}.{{end}}"}, "Ver pedido"},
	},
	KindOrderCancelled: {
		locale.ES:   {"Pedido {{.OrderNumber}} cancelado", "Pedido cancelado", []string{"Cancelamos tu pedido {{.OrderNumber}}.{{if .Reason}} Motivo: {{.Reason}}.{{end}} Si fue un error, puedes volver a hacerlo desde la tienda."}, "Ir a la tienda"},
		locale.EN:   {"Order {{.OrderNumber}} cancelled", "Order cancelled", []string{"Your order {{.OrderNumber}} was cancelled.{{if .Reason}} Reason: {{.Reason}}.{{end}} If this was a mistake, you can order again from the store."}, "Go to the store"},
		locale.PTBR: {"Pedido {{.OrderNumber}} cancelado", "Pedido cancelado", []string{"Seu pedido {{.OrderNumber}} foi cancelado.{{if .Reason}} Motivo: {{.Reason}}.{{end}} Se foi um engano, você pode refazê-lo na loja."}, "Ir para a loja"},
	},
	KindOrderRefunded: {
		locale.ES:   {"Reembolso del pedido {{.OrderNumber}}", "Reembolso en proceso", []string{"Cancelamos tu pedido {{.OrderNumber}} y reembolsamos {{.Total}}. Según tu banco, el monto puede tardar unos días en verse reflejado."}, "Ver pedido"},
		locale.EN:   {"Refund for order {{.OrderNumber}}", "Refund on its way", []string{"We cancelled your order {{.OrderNumber}} and refunded {{.Total}}. Depending on your bank, it may take a few days to show."}, "View order"},
		locale.PTBR: {"Reembolso do pedido {{.OrderNumber}}", "Reembolso em andamento", []string{"Cancelamos seu pedido {{.OrderNumber}} e reembolsamos {{.Total}}. Dependendo do seu banco, pode levar alguns dias para aparecer."}, "Ver pedido"},
	},
	KindInvoiceIssued: {
		locale.ES:   {"{{.DocumentName}} · pedido {{.OrderNumber}}", "Tu comprobante electrónico", []string{"Emitimos tu {{.DocumentName}} por el pedido {{.OrderNumber}}. Puedes descargarlo en PDF; la SUNAT ya lo recibió."}, "Descargar PDF"},
		locale.EN:   {"{{.DocumentName}} · order {{.OrderNumber}}", "Your electronic invoice", []string{"We issued your {{.DocumentName}} for order {{.OrderNumber}}. You can download the PDF; SUNAT has already received it."}, "Download PDF"},
		locale.PTBR: {"{{.DocumentName}} · pedido {{.OrderNumber}}", "Seu comprovante eletrônico", []string{"Emitimos seu {{.DocumentName}} do pedido {{.OrderNumber}}. Você pode baixá-lo em PDF; a SUNAT já o recebeu."}, "Baixar PDF"},
	},
	KindAdminStockLow: {
		locale.ES: {"Stock bajo: {{.SKU}}", "Stock bajo", []string{"La variante {{.SKU}} tiene {{.Available}} unidades disponibles."}, "Ver inventario"},
	},
	KindAdminInvoiceError: {
		locale.ES: {"Error al emitir comprobante · {{.OrderNumber}}", "Comprobante rechazado", []string{"No se pudo emitir el comprobante del pedido {{.OrderNumber}}: {{.Reason}}"}, "Ver pedido"},
	},
}

const layout = `<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>{{.Subject}}</title></head>
<body style="margin:0;background:#f7f9fb;font-family:Arial,Helvetica,sans-serif;color:#1d2733">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="padding:24px 12px"><tr><td align="center">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:560px;background:#ffffff;border:1px solid #e3e8ee;border-radius:12px">
<tr><td style="padding:24px 28px 8px">{{if .Logo}}<img src="{{.Logo}}" width="40" height="40" alt="" style="vertical-align:middle;border:0;margin-right:8px">{{end}}<span style="font-size:20px;font-weight:bold;color:#046880;vertical-align:middle">Duradent</span></td></tr>
<tr><td style="padding:8px 28px 24px">
<h1 style="font-size:20px;margin:0 0 12px">{{.Title}}</h1>
{{range .Body}}<p style="font-size:15px;line-height:1.55;margin:0 0 12px">{{.}}</p>{{end}}
{{if .Link}}<p style="margin:20px 0 4px"><a href="{{.Link}}" style="display:inline-block;background:#046880;color:#ffffff;text-decoration:none;font-weight:bold;padding:12px 20px;border-radius:8px">{{.Button}}</a></p>{{end}}
</td></tr>
<tr><td style="padding:16px 28px;border-top:1px solid #e3e8ee;font-size:12px;color:#52606d">Duradent SAC · Insumos de ortodoncia · Perú</td></tr>
</table></td></tr></table></body></html>`

var layoutTmpl = template.Must(template.New("layout").Parse(layout))

// Render builds an email. Unknown locales fall back to Spanish.
func Render(kind Kind, l locale.Locale, to string, d Data) (Message, error) {
	byLocale, ok := texts[kind]
	if !ok {
		return Message{}, fmt.Errorf("unknown email kind %q", kind)
	}
	c, ok := byLocale[l]
	if !ok {
		c, l = byLocale[locale.ES], locale.ES
	}
	exec := func(s string) (template.HTML, error) {
		t, err := template.New("").Parse(s)
		if err != nil {
			return "", err
		}
		var b bytes.Buffer
		if err := t.Execute(&b, d); err != nil {
			return "", err
		}
		return template.HTML(b.String()), nil //nolint:gosec // html/template escaped every field
	}
	subject, err := exec(c.Subject)
	if err != nil {
		return Message{}, err
	}
	title, err := exec(c.Title)
	if err != nil {
		return Message{}, err
	}
	body := make([]template.HTML, 0, len(c.Body))
	var text strings.Builder
	text.WriteString(plain(string(title)) + "\n\n")
	for _, p := range c.Body {
		h, err := exec(p)
		if err != nil {
			return Message{}, err
		}
		body = append(body, h)
		text.WriteString(plain(string(h)) + "\n\n")
	}
	if d.Link != "" {
		text.WriteString(c.Button + ": " + d.Link + "\n")
	}
	var out bytes.Buffer
	err = layoutTmpl.Execute(&out, map[string]any{
		"Lang": string(l), "Subject": plain(string(subject)), "Title": title, "Body": body, "Link": d.Link, "Button": c.Button,
		"Logo": d.LogoURL,
	})
	if err != nil {
		return Message{}, err
	}
	return Message{To: to, Subject: plain(string(subject)), HTML: out.String(), Text: text.String()}, nil
}

// plain turns the small HTML of the templates into text.
func plain(s string) string {
	r := strings.NewReplacer("<br>", "\n", "&#39;", "'", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&#34;", `"`, "&quot;", `"`)
	return r.Replace(s)
}
