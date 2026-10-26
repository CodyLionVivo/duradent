package domain

import (
	"regexp"
	"strings"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
)

var codeRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,31}$`)

// ValidCode reports whether s is a well-formed attribute or value code.
func ValidCode(s string) bool { return codeRe.MatchString(s) }

// Attribute is a dimension variants differ by: prescription (Roth, MBT),
// slot (.018, .022), tooth (11…47), size, material.
type Attribute struct {
	ID        id.ID
	Code      string
	Names     locale.Text
	SortOrder int
	Values    []AttributeValue
}

type AttributeValue struct {
	ID        id.ID
	Code      string
	Labels    locale.Text
	SortOrder int
}

func NewAttribute(code string, names locale.Text, sortOrder int) (*Attribute, error) {
	code = strings.TrimSpace(code)
	if !codeRe.MatchString(code) {
		return nil, apperr.Invalid("catalog.attribute_code_invalid", "code", "use lowercase letters, digits and '_'")
	}
	if strings.TrimSpace(names[locale.Default]) == "" {
		return nil, apperr.Invalid("catalog.name_required", "names.es", "a Spanish name is required")
	}
	return &Attribute{ID: id.New(), Code: code, Names: names, SortOrder: sortOrder}, nil
}

func (a *Attribute) Rename(names locale.Text, sortOrder int) error {
	if strings.TrimSpace(names[locale.Default]) == "" {
		return apperr.Invalid("catalog.name_required", "names.es", "a Spanish name is required")
	}
	a.Names, a.SortOrder = names, sortOrder
	return nil
}

func (a *Attribute) AddValue(code string, labels locale.Text) (AttributeValue, error) {
	code = strings.TrimSpace(code)
	if !codeRe.MatchString(code) {
		return AttributeValue{}, apperr.Invalid("catalog.attribute_value_invalid", "code", "use lowercase letters, digits and '_'")
	}
	if strings.TrimSpace(labels[locale.Default]) == "" {
		return AttributeValue{}, apperr.Invalid("catalog.name_required", "labels.es", "a Spanish label is required")
	}
	if _, ok := a.Value(code); ok {
		return AttributeValue{}, apperr.Conflict("catalog.attribute_value_taken", "code", "value already exists")
	}
	v := AttributeValue{ID: id.New(), Code: code, Labels: labels, SortOrder: len(a.Values)}
	a.Values = append(a.Values, v)
	return v, nil
}

func (a *Attribute) Value(code string) (AttributeValue, bool) {
	for _, v := range a.Values {
		if v.Code == code {
			return v, true
		}
	}
	return AttributeValue{}, false
}

// CheckOptions verifies that every option of a variant names a known
// attribute and one of its values.
func CheckOptions(attrs []*Attribute, options map[string]string) error {
	byCode := make(map[string]*Attribute, len(attrs))
	for _, a := range attrs {
		byCode[a.Code] = a
	}
	for k, v := range options {
		a, ok := byCode[k]
		if !ok {
			return apperr.Invalid("catalog.attribute_unknown", "options."+k, "unknown attribute")
		}
		if _, ok := a.Value(v); !ok {
			return apperr.Invalid("catalog.attribute_value_unknown", "options."+k, "unknown value "+v)
		}
	}
	return nil
}
