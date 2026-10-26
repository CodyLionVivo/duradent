// Package locale lists the languages the store is published in. Codes match
// the URL prefixes used by apps/web (/es, /en, /pt-br).
package locale

import (
	"errors"
	"fmt"
	"strings"
)

type Locale string

const (
	ES   Locale = "es"
	EN   Locale = "en"
	PTBR Locale = "pt-br"
)

// Default is the language every product must at least be written in.
const Default = ES

var All = []Locale{ES, EN, PTBR}

var ErrUnsupported = errors.New("unsupported locale")

func Parse(s string) (Locale, error) {
	l := Locale(strings.ToLower(strings.TrimSpace(s)))
	if l == "pt_br" || l == "pt" {
		l = PTBR
	}
	for _, a := range All {
		if l == a {
			return l, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrUnsupported, s)
}

// ParseOrDefault never fails: unknown or empty values fall back to Default.
func ParseOrDefault(s string) Locale {
	if l, err := Parse(s); err == nil {
		return l
	}
	return Default
}

// Text is a value translated into several locales.
type Text map[Locale]string

// In returns the text for l, falling back to the default locale.
func (t Text) In(l Locale) string {
	if v := t[l]; v != "" {
		return v
	}
	return t[Default]
}
