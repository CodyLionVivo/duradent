// Package seed loads a realistic demo catalog for local development: the
// usual orthodontic supplies with variants, in three languages, priced in
// three lists (public, clínica, distribuidor) in PEN and USD.
//
// It goes through the application services, so the data obeys the same rules
// as the admin panel. It does nothing if price lists already exist.
package seed

import (
	"context"
	"fmt"
	"log/slog"
	"math"

	catalogapp "github.com/CodyLionVivo/duradent/services/api/internal/catalog/application"
	"github.com/CodyLionVivo/duradent/services/api/internal/catalog/domain"
	inventoryapp "github.com/CodyLionVivo/duradent/services/api/internal/inventory/application"
	inventorydomain "github.com/CodyLionVivo/duradent/services/api/internal/inventory/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
	pricingapp "github.com/CodyLionVivo/duradent/services/api/internal/pricing/application"
	pricingdomain "github.com/CodyLionVivo/duradent/services/api/internal/pricing/domain"
)

type tr struct{ es, en, pt string }

func (t tr) text() locale.Text {
	return locale.Text{locale.ES: t.es, locale.EN: t.en, locale.PTBR: t.pt}
}

type attribute struct {
	code   string
	names  tr
	values []struct {
		code   string
		labels tr
	}
}

func val(code string, labels tr) struct {
	code   string
	labels tr
} {
	return struct {
		code   string
		labels tr
	}{code, labels}
}

var attributes = []attribute{
	{"prescription", tr{"Prescripción", "Prescription", "Prescrição"}, []struct {
		code   string
		labels tr
	}{
		val("roth", tr{"Roth", "Roth", "Roth"}),
		val("mbt", tr{"MBT", "MBT", "MBT"}),
	}},
	{"slot", tr{"Slot", "Slot", "Slot"}, []struct {
		code   string
		labels tr
	}{
		val("018", tr{`.018"`, `.018"`, `.018"`}),
		val("022", tr{`.022"`, `.022"`, `.022"`}),
	}},
	{"wire_size", tr{"Calibre", "Wire size", "Calibre"}, []struct {
		code   string
		labels tr
	}{
		val("012", tr{`.012"`, `.012"`, `.012"`}),
		val("014", tr{`.014"`, `.014"`, `.014"`}),
		val("016", tr{`.016"`, `.016"`, `.016"`}),
		val("018", tr{`.018"`, `.018"`, `.018"`}),
		val("016x022", tr{`.016 × .022"`, `.016 × .022"`, `.016 × .022"`}),
		val("017x025", tr{`.017 × .025"`, `.017 × .025"`, `.017 × .025"`}),
		val("019x025", tr{`.019 × .025"`, `.019 × .025"`, `.019 × .025"`}),
	}},
	{"arch", tr{"Arcada", "Arch", "Arcada"}, []struct {
		code   string
		labels tr
	}{
		val("upper", tr{"Superior", "Upper", "Superior"}),
		val("lower", tr{"Inferior", "Lower", "Inferior"}),
	}},
	{"color", tr{"Color", "Color", "Cor"}, []struct {
		code   string
		labels tr
	}{
		val("clear", tr{"Transparente", "Clear", "Transparente"}),
		val("gray", tr{"Gris", "Gray", "Cinza"}),
		val("blue", tr{"Azul", "Blue", "Azul"}),
		val("pink", tr{"Rosado", "Pink", "Rosa"}),
	}},
	{"elastic_size", tr{"Diámetro", "Diameter", "Diâmetro"}, []struct {
		code   string
		labels tr
	}{
		val("1_8", tr{`1/8"`, `1/8"`, `1/8"`}),
		val("3_16", tr{`3/16"`, `3/16"`, `3/16"`}),
		val("1_4", tr{`1/4"`, `1/4"`, `1/4"`}),
		val("5_16", tr{`5/16"`, `5/16"`, `5/16"`}),
	}},
}

type category struct {
	slug, parent string
	names        tr
	desc         tr
}

var categories = []category{
	{"brackets", "", tr{"Brackets", "Brackets", "Braquetes"}, tr{"Brackets metálicos, estéticos y de autoligado.", "Metal, aesthetic and self-ligating brackets.", "Braquetes metálicos, estéticos e autoligáveis."}},
	{"brackets-metalicos", "brackets", tr{"Metálicos", "Metal", "Metálicos"}, tr{}},
	{"brackets-esteticos", "brackets", tr{"Estéticos", "Aesthetic", "Estéticos"}, tr{}},
	{"brackets-autoligado", "brackets", tr{"Autoligado", "Self-ligating", "Autoligáveis"}, tr{}},
	{"tubos-bandas", "", tr{"Tubos y bandas", "Tubes and bands", "Tubos e bandas"}, tr{"Tubos bucales y bandas molares.", "Buccal tubes and molar bands.", "Tubos bucais e bandas molares."}},
	{"arcos", "", tr{"Arcos y alambres", "Archwires", "Arcos e fios"}, tr{"Arcos preformados de NiTi, acero y TMA.", "Preformed NiTi, steel and TMA archwires.", "Arcos pré-formados de NiTi, aço e TMA."}},
	{"elasticos", "", tr{"Elásticos y ligaduras", "Elastics and ligatures", "Elásticos e ligaduras"}, tr{"Ligaduras, cadenas e intermaxilares.", "Ligatures, power chains and intermaxillary elastics.", "Ligaduras, correntes e elásticos intermaxilares."}},
	{"instrumental", "", tr{"Instrumental", "Instruments", "Instrumental"}, tr{"Pinzas y alicates de ortodoncia.", "Orthodontic pliers and cutters.", "Alicates ortodônticos."}},
	{"adhesivos", "", tr{"Adhesivos", "Adhesives", "Adesivos"}, tr{"Adhesivos y acondicionadores para cementado.", "Bonding adhesives and conditioners.", "Adesivos e condicionadores para colagem."}},
}

type variant struct {
	sku     string
	weightG int
	options map[string]string
}

type product struct {
	slug, category, brand string
	names, desc           tr
	pen                   string // public unit price in PEN
	variants              []variant
}

// combos expands option values into variants: SKU = prefix + values.
func combos(prefix string, weight int, dims ...struct {
	attr   string
	values []string
}) []variant {
	out := []variant{{sku: prefix, weightG: weight, options: map[string]string{}}}
	for _, d := range dims {
		var next []variant
		for _, v := range out {
			for _, value := range d.values {
				opts := map[string]string{d.attr: value}
				for k, o := range v.options {
					opts[k] = o
				}
				next = append(next, variant{sku: v.sku + "-" + value, weightG: weight, options: opts})
			}
		}
		out = next
	}
	return out
}

func dim(attr string, values ...string) struct {
	attr   string
	values []string
} {
	return struct {
		attr   string
		values []string
	}{attr, values}
}

func single(sku string, weight int) []variant {
	return []variant{{sku: sku, weightG: weight}}
}

var products = []product{
	{"kit-brackets-metalicos-roth", "brackets-metalicos", "Duradent",
		tr{"Kit de brackets metálicos Roth (20 piezas)", "Roth metal bracket kit (20 pieces)", "Kit de braquetes metálicos Roth (20 peças)"},
		tr{"Brackets de acero inoxidable 17-4 con base de malla, caso completo 5-5 superior e inferior con ganchos en 3-4-5.",
			"17-4 stainless steel brackets with mesh base, full 5-5 upper and lower case with hooks on 3-4-5.",
			"Braquetes de aço inoxidável 17-4 com base de malha, caso completo 5-5 superior e inferior com ganchos em 3-4-5."},
		"85.00", combos("BRK-MET-ROTH", 18, dim("slot", "018", "022"))},
	{"kit-brackets-metalicos-mbt", "brackets-metalicos", "Duradent",
		tr{"Kit de brackets metálicos MBT (20 piezas)", "MBT metal bracket kit (20 pieces)", "Kit de braquetes metálicos MBT (20 peças)"},
		tr{"Prescripción MBT con torque incrementado en incisivos. Base de malla de alta retención.",
			"MBT prescription with increased incisor torque. High-retention mesh base.",
			"Prescrição MBT com torque aumentado nos incisivos. Base de malha de alta retenção."},
		"85.00", combos("BRK-MET-MBT", 18, dim("slot", "018", "022"))},
	{"kit-brackets-mini-roth", "brackets-metalicos", "Duradent",
		tr{"Kit de brackets mini Roth (20 piezas)", "Mini Roth bracket kit (20 pieces)", "Kit de braquetes mini Roth (20 peças)"},
		tr{"Perfil reducido para mayor confort del paciente e higiene.", "Low profile for patient comfort and easier hygiene.", "Perfil reduzido para maior conforto e higiene do paciente."},
		"98.00", combos("BRK-MINI-ROTH", 15, dim("slot", "018", "022"))},
	{"kit-brackets-ceramicos-roth", "brackets-esteticos", "Duradent Clear",
		tr{"Kit de brackets cerámicos Roth (20 piezas)", "Roth ceramic bracket kit (20 pieces)", "Kit de braquetes cerâmicos Roth (20 peças)"},
		tr{"Cerámica policristalina translúcida, resistente a manchas. Ranura .022.",
			"Translucent polycrystalline ceramic, stain resistant. .022 slot.",
			"Cerâmica policristalina translúcida, resistente a manchas. Slot .022."},
		"290.00", combos("BRK-CER-ROTH", 16, dim("slot", "022"))},
	{"kit-brackets-ceramicos-mbt", "brackets-esteticos", "Duradent Clear",
		tr{"Kit de brackets cerámicos MBT (20 piezas)", "MBT ceramic bracket kit (20 pieces)", "Kit de braquetes cerâmicos MBT (20 peças)"},
		tr{"Estética y control de torque MBT con ranura reforzada.", "Aesthetics with MBT torque control and reinforced slot.", "Estética com controle de torque MBT e slot reforçado."},
		"290.00", combos("BRK-CER-MBT", 16, dim("slot", "022"))},
	{"kit-brackets-autoligado-pasivo", "brackets-autoligado", "Duradent SL",
		tr{"Kit de brackets de autoligado pasivo (20 piezas)", "Passive self-ligating bracket kit (20 pieces)", "Kit de braquetes autoligáveis passivos (20 peças)"},
		tr{"Puerta deslizante de níquel-titanio, baja fricción y menos citas de control.",
			"Nickel-titanium sliding door, low friction and fewer adjustment visits.",
			"Porta deslizante de níquel-titânio, baixo atrito e menos consultas de ajuste."},
		"420.00", combos("BRK-SL", 20, dim("prescription", "roth", "mbt"), dim("slot", "022"))},
	{"tubos-bucales-primer-molar", "tubos-bandas", "Duradent",
		tr{"Tubos bucales adheribles 1.ª molar (4 piezas)", "Bondable 1st molar buccal tubes (4 pieces)", "Tubos bucais coláveis 1.º molar (4 peças)"},
		tr{"Tubo simple convertible con base de malla anatómica.", "Single convertible tube with anatomical mesh base.", "Tubo simples conversível com base de malha anatômica."},
		"32.00", combos("TUB-1M", 4, dim("prescription", "roth", "mbt"), dim("slot", "022"))},
	{"bandas-molares-con-tubo", "tubos-bandas", "Duradent",
		tr{"Bandas molares con tubo soldado (10 piezas)", "Molar bands with welded tube (10 pieces)", "Bandas molares com tubo soldado (10 peças)"},
		tr{"Bandas preformadas de acero, surtido de tallas 32 a 40.", "Preformed steel bands, assorted sizes 32 to 40.", "Bandas pré-formadas de aço, tamanhos sortidos de 32 a 40."},
		"58.00", combos("BND-TUB", 12, dim("arch", "upper", "lower"))},
	{"arco-niti-termoactivado-redondo", "arcos", "Duradent",
		tr{"Arco NiTi termoactivado redondo (10 unidades)", "Heat-activated round NiTi archwire (10 units)", "Arco NiTi termoativado redondo (10 unidades)"},
		tr{"Fuerza ligera y constante a temperatura corporal. Forma de arco ovoide.",
			"Light, constant force at body temperature. Ovoid arch form.",
			"Força leve e constante à temperatura corporal. Forma de arco ovoide."},
		"38.00", combos("ARC-NITI-TA", 8, dim("wire_size", "012", "014", "016", "018"), dim("arch", "upper", "lower"))},
	{"arco-niti-rectangular", "arcos", "Duradent",
		tr{"Arco NiTi rectangular (10 unidades)", "Rectangular NiTi archwire (10 units)", "Arco NiTi retangular (10 unidades)"},
		tr{"Superelástico para nivelación y control de torque inicial.", "Superelastic for levelling and early torque control.", "Superelástico para nivelamento e controle de torque inicial."},
		"45.00", combos("ARC-NITI-RC", 10, dim("wire_size", "016x022", "017x025", "019x025"), dim("arch", "upper", "lower"))},
	{"arco-acero-rectangular", "arcos", "Duradent",
		tr{"Arco de acero inoxidable rectangular (10 unidades)", "Rectangular stainless steel archwire (10 units)", "Arco de aço inoxidável retangular (10 unidades)"},
		tr{"Arco de trabajo para mecánica de deslizamiento y cierre de espacios.", "Working wire for sliding mechanics and space closure.", "Arco de trabalho para mecânica de deslizamento e fechamento de espaços."},
		"30.00", combos("ARC-SS-RC", 12, dim("wire_size", "016x022", "017x025", "019x025"), dim("arch", "upper", "lower"))},
	{"arco-tma", "arcos", "Duradent",
		tr{"Arco TMA beta-titanio (5 unidades)", "TMA beta-titanium archwire (5 units)", "Arco TMA beta-titânio (5 unidades)"},
		tr{"Formabilidad y fuerza intermedia entre NiTi y acero, ideal para finalización.",
			"Formability and force between NiTi and steel, ideal for finishing.",
			"Moldabilidade e força intermediária entre NiTi e aço, ideal para finalização."},
		"75.00", combos("ARC-TMA", 6, dim("wire_size", "017x025", "019x025"), dim("arch", "upper", "lower"))},
	{"ligaduras-elasticas", "elasticos", "Duradent",
		tr{"Ligaduras elásticas (1000 unidades)", "Elastic ligatures (1000 units)", "Ligaduras elásticas (1000 unidades)"},
		tr{"Módulos de poliuretano de grado médico en carrete.", "Medical-grade polyurethane modules on sticks.", "Módulos de poliuretano de grau médico em bastões."},
		"36.00", combos("LIG-ELA", 30, dim("color", "clear", "gray", "blue", "pink"))},
	{"cadena-elastica", "elasticos", "Duradent",
		tr{"Cadena elástica corta (rollo 4,5 m)", "Short power chain (4.5 m spool)", "Corrente elástica curta (rolo 4,5 m)"},
		tr{"Para cierre de espacios y rotaciones. Memoria elástica prolongada.", "For space closure and rotations. Long-lasting elastic memory.", "Para fechamento de espaços e rotações. Memória elástica prolongada."},
		"22.00", combos("CAD-ELA", 25, dim("color", "clear", "gray"))},
	{"elasticos-intermaxilares", "elasticos", "Duradent",
		tr{"Elásticos intermaxilares fuerza media (100 unidades)", "Medium-force intermaxillary elastics (100 units)", "Elásticos intermaxilares força média (100 unidades)"},
		tr{"Látex de grado médico, 4,5 oz.", "Medical-grade latex, 4.5 oz.", "Látex de grau médico, 4,5 oz."},
		"9.50", combos("ELA-IM", 6, dim("elastic_size", "1_8", "3_16", "1_4", "5_16"))},
	{"pinza-how-recta", "instrumental", "Duradent Pro",
		tr{"Pinza How recta", "Straight How plier", "Alicate How reto"},
		tr{"Acero inoxidable quirúrgico, puntas estriadas para sujetar arcos y ligaduras.",
			"Surgical stainless steel, serrated tips to hold wires and ligatures.",
			"Aço inoxidável cirúrgico, pontas serrilhadas para segurar fios e ligaduras."},
		"95.00", single("INS-HOW-R", 90)},
	{"alicate-corte-distal", "instrumental", "Duradent Pro",
		tr{"Alicate de corte distal con seguro", "Safety-hold distal end cutter", "Alicate de corte distal com retenção"},
		tr{"Corta y retiene el extremo del arco hasta .021 × .025.", "Cuts and holds the wire end up to .021 × .025.", "Corta e retém a ponta do arco até .021 × .025."},
		"180.00", single("INS-CORTE-D", 110)},
	{"pinza-posicionadora-brackets", "instrumental", "Duradent Pro",
		tr{"Pinza posicionadora de brackets", "Bracket positioning tweezer", "Pinça posicionadora de braquetes"},
		tr{"Puntas de agarre invertido para colocación precisa.", "Reverse-action tips for precise placement.", "Pontas de ação inversa para posicionamento preciso."},
		"48.00", single("INS-POS-BRK", 40)},
	{"adhesivo-fotocurable-brackets", "adhesivos", "Duradent Bond",
		tr{"Adhesivo fotocurable para brackets (jeringa 4 g)", "Light-cure bracket adhesive (4 g syringe)", "Adesivo fotopolimerizável para braquetes (seringa 4 g)"},
		tr{"Viscosidad que no escurre, tiempo de trabajo extendido.", "Non-slump viscosity, extended working time.", "Viscosidade que não escorre, tempo de trabalho estendido."},
		"110.00", single("ADH-FOTO-4G", 25)},
	{"acido-grabador-37", "adhesivos", "Duradent Bond",
		tr{"Ácido grabador fosfórico 37 % (jeringa 5 ml)", "37% phosphoric acid etchant (5 ml syringe)", "Ácido fosfórico 37 % (seringa 5 ml)"},
		tr{"Gel tixotrópico azul con puntas aplicadoras.", "Blue thixotropic gel with applicator tips.", "Gel tixotrópico azul com pontas aplicadoras."},
		"18.00", single("ADH-ACID-37", 15)},
}

// price lists: multiplier over the public PEN price.
var lists = []struct {
	code, name string
	factor     float64
	isDefault  bool
}{
	{"public", "Público", 1.00, true},
	{"clinica", "Clínicas y consultorios", 0.88, false},
	{"distribuidor", "Distribuidores", 0.75, false},
}

const penPerUSD = 3.75

func Run(ctx context.Context, log *slog.Logger, catalog *catalogapp.Service, pricing *pricingapp.Service, inventory *inventoryapp.Service) error {
	if existing, err := pricing.PriceLists(ctx); err != nil {
		return err
	} else if len(existing) > 0 {
		log.Info("seed skipped: data already present")
		return nil
	}

	for i, a := range attributes {
		if _, err := catalog.CreateAttribute(ctx, a.code, a.names.text(), i); err != nil {
			return fmt.Errorf("attribute %s: %w", a.code, err)
		}
		for _, v := range a.values {
			if _, err := catalog.AddAttributeValue(ctx, a.code, v.code, v.labels.text()); err != nil {
				return fmt.Errorf("attribute value %s=%s: %w", a.code, v.code, err)
			}
		}
	}

	catIDs := map[string]id.ID{}
	for i, c := range categories {
		created, err := catalog.CreateCategory(ctx, domain.CategoryDetails{
			Slug: c.slug, ParentID: catIDs[c.parent], SortOrder: i, Texts: translations(c.names, c.desc),
		})
		if err != nil {
			return fmt.Errorf("category %s: %w", c.slug, err)
		}
		catIDs[c.slug] = created.ID
	}

	listIDs := map[string]id.ID{}
	for _, l := range lists {
		pl, err := pricing.CreatePriceList(ctx, l.code, l.name, l.isDefault)
		if err != nil {
			return fmt.Errorf("price list %s: %w", l.code, err)
		}
		listIDs[l.code] = pl.ID
	}

	variants := 0
	for pi, p := range products {
		created, err := catalog.CreateProduct(ctx, domain.Details{
			Slug: p.slug, Brand: p.brand, CategoryID: catIDs[p.category], Texts: translations(p.names, p.desc),
		})
		if err != nil {
			return fmt.Errorf("product %s: %w", p.slug, err)
		}
		for _, v := range p.variants {
			if created, err = catalog.AddVariant(ctx, created.ID, domain.VariantInput{SKU: v.sku, WeightG: v.weightG, Options: v.options}); err != nil {
				return fmt.Errorf("variant %s: %w", v.sku, err)
			}
		}
		if _, err := catalog.ChangeStatus(ctx, created.ID, domain.StatusActive); err != nil {
			return fmt.Errorf("publish %s: %w", p.slug, err)
		}

		base, err := money.Parse(p.pen, money.PEN)
		if err != nil {
			return err
		}
		for vi, v := range created.ActiveVariants() {
			// Varied demo stock, with one sold-out variant to see that state.
			stock := 20 + (pi*37+vi*11)%180
			if pi == 1 && vi == 1 {
				stock = 0
			}
			if stock > 0 {
				if _, err := inventory.Adjust(ctx, v.ID, stock, inventorydomain.MovementPurchase, "stock inicial (demo)"); err != nil {
					return fmt.Errorf("stock %s: %w", v.SKU, err)
				}
			}
			if _, err := inventory.SetReorderPoint(ctx, v.ID, 10); err != nil {
				return err
			}
			for _, l := range lists {
				if _, err := pricing.SetVariantPrices(ctx, listIDs[l.code], v.ID, tiers(base, l.factor)); err != nil {
					return fmt.Errorf("prices %s/%s: %w", v.SKU, l.code, err)
				}
			}
			variants++
		}
	}
	log.Info("seed loaded", "categories", len(categories), "products", len(products), "variants", variants, "price_lists", len(lists))
	return nil
}

func translations(names, desc tr) map[locale.Locale]domain.Translation {
	return map[locale.Locale]domain.Translation{
		locale.ES:   {Name: names.es, Description: desc.es},
		locale.EN:   {Name: names.en, Description: desc.en},
		locale.PTBR: {Name: names.pt, Description: desc.pt},
	}
}

// tiers derives a list's prices from the public PEN price: PEN and USD at
// quantity 1, plus a volume tier at 10 units with 8% off.
func tiers(publicPEN money.Money, factor float64) []pricingdomain.Tier {
	pen := round(float64(publicPEN.Minor) * factor)
	usd := round(float64(pen) / penPerUSD)
	return []pricingdomain.Tier{
		{MinQty: 1, Unit: money.New(pen, money.PEN)},
		{MinQty: 10, Unit: money.New(round(float64(pen)*0.92), money.PEN)},
		{MinQty: 1, Unit: money.New(usd, money.USD)},
		{MinQty: 10, Unit: money.New(round(float64(usd)*0.92), money.USD)},
	}
}

// round rounds minor units to the nearest 10 céntimos so prices look tidy.
func round(minor float64) int64 {
	return int64(math.Round(minor/10) * 10)
}
