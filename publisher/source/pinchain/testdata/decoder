package dalgo2http

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// ErrInvalidXML indicates a response violated the named XML decoding contract.
// Errors deliberately omit source values and parser snippets.
var ErrInvalidXML = errors.New("dalgo2http: invalid XML response")

const (
	gesmesNamespace = "http://www.gesmes.org/xml/2002-08-01"
	ecbNamespace    = "http://www.ecb.int/vocabulary/2002-08-01/eurofxref"
	maxECBRows      = 256
)

var (
	currencyCode   = regexp.MustCompile(`^[A-Z]{3}$`)
	lexicalDecimal = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)
)

// decodeECBDaily is namespace-aware and accepts one Envelope/Cube/Cube(time)
// with bounded quote children. Rates stay lexical strings, without float
// conversion, extra EUR rows, cross-rates, or retained source bytes.
func decodeECBDaily(body []byte) ([]map[string]any, error) {
	if len(body) > maxBodyBytes {
		return nil, ErrResponseTooLarge
	}
	if bytes.Contains(body, []byte("&")) {
		return nil, fmt.Errorf("%w: ecb-eurofxref/1: entity references are forbidden", ErrInvalidXML)
	}
	decoder := xml.NewDecoder(bytes.NewReader(body))
	var stack []xml.Name
	var rows []map[string]any
	seen := map[string]bool{}
	var date string
	var roots, cubes, dates, subjects, senders, names int
	fail := func(reason string) ([]map[string]any, error) {
		return nil, fmt.Errorf("%w: ecb-eurofxref/1: %s", ErrInvalidXML, reason)
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fail("malformed XML")
		}
		switch t := token.(type) {
		case xml.StartElement:
			depth := len(stack)
			if depth >= 4 {
				return fail("depth exceeds daily structure")
			}
			attrs := map[string]string{}
			attributeNames := map[xml.Name]bool{}
			for _, attr := range t.Attr {
				if attributeNames[attr.Name] {
					return fail("duplicate attribute")
				}
				attributeNames[attr.Name] = true
				if attr.Name.Space == "xmlns" || attr.Name == (xml.Name{Local: "xmlns"}) {
					continue
				}
				if attr.Name.Space != "" {
					return fail("namespaced attribute")
				}
				attrs[attr.Name.Local] = attr.Value
			}
			switch {
			case depth == 0 && t.Name == (xml.Name{Space: gesmesNamespace, Local: "Envelope"}):
				roots++
				if roots != 1 || len(attrs) != 0 {
					return fail("invalid envelope")
				}
			case depth == 1 && t.Name == (xml.Name{Space: gesmesNamespace, Local: "subject"}):
				subjects++
				if subjects != 1 || len(attrs) != 0 {
					return fail("invalid subject")
				}
			case depth == 1 && t.Name == (xml.Name{Space: gesmesNamespace, Local: "Sender"}):
				senders++
				if senders != 1 || len(attrs) != 0 {
					return fail("invalid sender")
				}
			case depth == 2 && stack[1] == (xml.Name{Space: gesmesNamespace, Local: "Sender"}) && t.Name == (xml.Name{Space: gesmesNamespace, Local: "name"}):
				names++
				if names != 1 || len(attrs) != 0 {
					return fail("invalid sender name")
				}
			case t.Name == (xml.Name{Space: ecbNamespace, Local: "Cube"}) && depth == 1:
				cubes++
				if cubes != 1 || len(attrs) != 0 {
					return fail("invalid outer cube")
				}
			case t.Name == (xml.Name{Space: ecbNamespace, Local: "Cube"}) && depth == 2 && stack[1] == (xml.Name{Space: ecbNamespace, Local: "Cube"}):
				dates++
				date = attrs["time"]
				if dates != 1 || len(attrs) != 1 || len(date) != 10 {
					return fail("daily response requires one date")
				}
				if _, err := time.Parse("2006-01-02", date); err != nil {
					return fail("invalid reference date")
				}
			case t.Name == (xml.Name{Space: ecbNamespace, Local: "Cube"}) && depth == 3 && stack[2] == (xml.Name{Space: ecbNamespace, Local: "Cube"}):
				currency, rate := attrs["currency"], attrs["rate"]
				if len(attrs) != 2 || !currencyCode.MatchString(currency) || currency == "EUR" || !lexicalDecimal.MatchString(rate) || strings.Trim(rate, "0.") == "" {
					return fail("invalid quote attributes")
				}
				if seen[currency] || len(rows) >= maxECBRows {
					return fail("duplicate currency or row bound exceeded")
				}
				seen[currency] = true
				rows = append(rows, map[string]any{"time": date, "currency": currency, "rate": rate})
			default:
				return fail("unexpected element or namespace")
			}
			stack = append(stack, t.Name)
		case xml.EndElement:
			stack = stack[:len(stack)-1] // Decoder.Token enforces matching XML.
		case xml.CharData:
			if strings.TrimSpace(string(t)) != "" && (len(stack) == 0 || (stack[len(stack)-1].Local != "subject" && stack[len(stack)-1].Local != "name")) {
				return fail("unexpected text")
			}
		case xml.Directive:
			return fail("DTD and directives are forbidden")
		case xml.ProcInst:
			if t.Target != "xml" || roots != 0 {
				return fail("processing instructions are forbidden")
			}
		}
	}
	if roots != 1 || cubes != 1 || dates != 1 || len(rows) == 0 {
		return fail("incomplete daily response")
	}
	return rows, nil
}
