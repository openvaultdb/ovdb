package dalgo2http

import (
	"fmt"
	"net/url"
)

// Decoder identifies a supported, versioned response contract. No executable
// transforms or caller-provided XML mappings are admitted.
type Decoder string

const (
	DecoderJSON         Decoder = "json"
	DecoderECBEuroFXRef Decoder = "ecb-eurofxref/1"
)

func (coll Collection) validateDecoder() error {
	switch coll.Decoder {
	case "", DecoderJSON:
		return nil
	case DecoderECBEuroFXRef:
		if coll.RowsPath != "" || coll.KeyField != "currency" || len(coll.Params) != 0 || coll.Timeout <= 0 {
			return fmt.Errorf("%w: collection %q: ECB daily decoding requires keyField currency, no rowsPath or params, and a positive timeout", ErrInvalidConfig, coll.Name)
		}
		parsed, err := url.Parse(coll.URLTemplate)
		if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || len(coll.Headers) != 0 {
			return fmt.Errorf("%w: collection %q: ECB daily decoding requires a fixed public URL without credentials, query, fragment, or secret headers", ErrInvalidConfig, coll.Name)
		}
		return nil
	default:
		return fmt.Errorf("%w: collection %q: decoder %q is not supported", ErrInvalidConfig, coll.Name, coll.Decoder)
	}
}

func decodeRows(body []byte, coll Collection) ([]map[string]any, error) {
	if coll.Decoder == DecoderECBEuroFXRef {
		return decodeECBDaily(body)
	}
	return extractRows(body, coll.RowsPath)
}
