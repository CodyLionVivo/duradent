package domain

import (
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
)

// Complaint is a sheet of the Libro de Reclamaciones as the complaints
// context publishes it (ComplaintFiled, ComplaintAnswered). Emails carry a
// full copy of it: the consumer's proof of what they filed.
type Complaint struct {
	ID              string
	Code            string // "000001-2026"
	Kind            string // reclamo | queja
	Locale          locale.Locale
	FiledAt         time.Time
	DueAt           time.Time
	ProviderName    string
	ProviderRUC     string
	ProviderAddress string
	Name            string
	DocType         string
	DocNumber       string
	Address         string
	Phone           string
	Email           string
	Guardian        string
	ItemType        string // producto | servicio
	OrderNumber     string
	Amount          *money.Money
	ItemDescription string
	Detail          string
	Request         string
	Channel         string // email | address
	Response        string
	AnsweredAt      *time.Time
}

// Row is a line of the sheet in an email; section rows are headings.
type Row struct {
	Label   string
	Value   string
	Section bool
}

type sheetLabels struct {
	Sheet, Date, Provider                                    string
	ConsumerSection, Name, Document, Address, Phone, Email   string
	Guardian, ItemSection, ItemType, Product, Service, Order string
	Amount, Description, DetailSection, Kind, Claim          string
	Complaint, Detail, Request, Channel, ByEmail, ByLetter   string
	AnswerSection, Response, AnsweredAt                      string
}

var labels = map[locale.Locale]sheetLabels{
	locale.ES: {
		Sheet: "Hoja de reclamación N.°", Date: "Fecha", Provider: "Proveedor",
		ConsumerSection: "1. Identificación del consumidor reclamante", Name: "Nombre", Document: "Documento",
		Address: "Domicilio", Phone: "Teléfono", Email: "Correo electrónico", Guardian: "Padre, madre o apoderado (menores de edad)",
		ItemSection: "2. Identificación del bien contratado", ItemType: "Bien contratado", Product: "Producto", Service: "Servicio",
		Order: "N.° de pedido", Amount: "Monto reclamado", Description: "Descripción",
		DetailSection: "3. Detalle de la reclamación y pedido del consumidor", Kind: "Tipo",
		Claim:     "Reclamo: disconformidad relacionada a los productos o servicios",
		Complaint: "Queja: disconformidad no relacionada a los productos o servicios, o malestar o descontento respecto a la atención al público",
		Detail:    "Detalle", Request: "Pedido", Channel: "Respuesta por", ByEmail: "Correo electrónico", ByLetter: "Carta al domicilio",
		AnswerSection: "4. Observaciones y acciones adoptadas por el proveedor", Response: "Respuesta", AnsweredAt: "Fecha de la respuesta",
	},
	locale.EN: {
		Sheet: "Complaint sheet No.", Date: "Date", Provider: "Provider",
		ConsumerSection: "1. Consumer filing the claim", Name: "Name", Document: "ID document",
		Address: "Address", Phone: "Phone", Email: "Email", Guardian: "Parent or guardian (minors)",
		ItemSection: "2. Product or service purchased", ItemType: "Purchased", Product: "Product", Service: "Service",
		Order: "Order no.", Amount: "Amount claimed", Description: "Description",
		DetailSection: "3. Details of the claim and consumer's request", Kind: "Type",
		Claim:     "Claim (reclamo): disagreement about the products or services",
		Complaint: "Complaint (queja): disagreement not about the products or services, or dissatisfaction with customer service",
		Detail:    "Details", Request: "Request", Channel: "Answer by", ByEmail: "Email", ByLetter: "Letter to the address",
		AnswerSection: "4. Provider's observations and actions taken", Response: "Answer", AnsweredAt: "Answer date",
	},
	locale.PTBR: {
		Sheet: "Folha de reclamação n.º", Date: "Data", Provider: "Fornecedor",
		ConsumerSection: "1. Identificação do consumidor reclamante", Name: "Nome", Document: "Documento",
		Address: "Endereço", Phone: "Telefone", Email: "E-mail", Guardian: "Pai, mãe ou responsável (menores de idade)",
		ItemSection: "2. Identificação do bem contratado", ItemType: "Bem contratado", Product: "Produto", Service: "Serviço",
		Order: "N.º do pedido", Amount: "Valor reclamado", Description: "Descrição",
		DetailSection: "3. Detalhe da reclamação e pedido do consumidor", Kind: "Tipo",
		Claim:     "Reclamação (reclamo): desacordo relacionado aos produtos ou serviços",
		Complaint: "Queixa (queja): desacordo não relacionado aos produtos ou serviços, ou insatisfação com o atendimento ao público",
		Detail:    "Detalhe", Request: "Pedido", Channel: "Resposta por", ByEmail: "E-mail", ByLetter: "Carta para o endereço",
		AnswerSection: "4. Observações e ações adotadas pelo fornecedor", Response: "Resposta", AnsweredAt: "Data da resposta",
	},
}

// ComplaintKind is "reclamo"/"queja" as a word for the email's sentences.
func ComplaintKind(kind string, l locale.Locale) string {
	words := map[locale.Locale][2]string{
		locale.ES: {"reclamo", "queja"}, locale.EN: {"claim", "complaint"}, locale.PTBR: {"reclamação", "queixa"},
	}
	w, ok := words[l]
	if !ok {
		w = words[locale.ES]
	}
	if kind == "queja" {
		return w[1]
	}
	return w[0]
}

// ComplaintRows lays out the sheet in the reader's language, in the order of
// the official form.
func ComplaintRows(c Complaint, l locale.Locale) []Row {
	lb, ok := labels[l]
	if !ok {
		l, lb = locale.ES, labels[locale.ES]
	}
	pick := func(cond bool, a, b string) string {
		if cond {
			return a
		}
		return b
	}
	rows := []Row{
		{Label: lb.Sheet + " " + c.Code, Section: true},
		{Label: lb.Date, Value: FormatDeadline(c.FiledAt, l)},
		{Label: lb.Provider, Value: c.ProviderName + " · RUC " + c.ProviderRUC + "\n" + c.ProviderAddress},
		{Label: lb.ConsumerSection, Section: true},
		{Label: lb.Name, Value: c.Name},
		{Label: lb.Document, Value: c.DocType + " " + c.DocNumber},
		{Label: lb.Address, Value: c.Address},
	}
	if c.Phone != "" {
		rows = append(rows, Row{Label: lb.Phone, Value: c.Phone})
	}
	rows = append(rows, Row{Label: lb.Email, Value: c.Email})
	if c.Guardian != "" {
		rows = append(rows, Row{Label: lb.Guardian, Value: c.Guardian})
	}
	rows = append(rows, Row{Label: lb.ItemSection, Section: true},
		Row{Label: lb.ItemType, Value: pick(c.ItemType == "servicio", lb.Service, lb.Product)})
	if c.OrderNumber != "" {
		rows = append(rows, Row{Label: lb.Order, Value: c.OrderNumber})
	}
	if c.Amount != nil {
		rows = append(rows, Row{Label: lb.Amount, Value: FormatMoney(*c.Amount)})
	}
	rows = append(rows, Row{Label: lb.Description, Value: c.ItemDescription},
		Row{Label: lb.DetailSection, Section: true},
		Row{Label: lb.Kind, Value: pick(c.Kind == "queja", lb.Complaint, lb.Claim)},
		Row{Label: lb.Detail, Value: c.Detail},
		Row{Label: lb.Request, Value: c.Request},
		Row{Label: lb.Channel, Value: pick(c.Channel == "address", lb.ByLetter, lb.ByEmail)})
	if c.AnsweredAt != nil {
		rows = append(rows, Row{Label: lb.AnswerSection, Section: true},
			Row{Label: lb.Response, Value: c.Response},
			Row{Label: lb.AnsweredAt, Value: FormatDeadline(*c.AnsweredAt, l)})
	}
	return rows
}
