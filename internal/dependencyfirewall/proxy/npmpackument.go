package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"slices"
)

// packument is the part of an npm publish body the matcher checks.
type packument struct {
	name        string
	versions    []string
	attachments bool
}

var errMalformedPackument = errors.New("malformed npm packument")

// maxPackumentKey bounds a top-level key; npm's are short field names.
const maxPackumentKey = 4 << 10

// scanPackument reads the top-level keys of a publish body from r without
// holding the body in memory, so a tarball larger than the inspection window
// does not hide the coordinate. Only name and versions are decoded, each
// capped at maxValue bytes; every other value, including the base64 tarball
// in _attachments, is skipped byte by byte. A repeated key replaces the
// earlier one, as it does for the registry's JSON parser.
func scanPackument(r io.Reader, maxValue int) (packument, error) {
	br := bufio.NewReader(r)
	var p packument
	if c, err := nextNonSpace(br); err != nil || c != '{' {
		return p, errMalformedPackument
	}
	c, err := nextNonSpace(br)
	if err != nil {
		return p, err
	}
	if c == '}' {
		return p, nil
	}
	for {
		if c != '"' {
			return p, errMalformedPackument
		}
		var raw bytes.Buffer
		raw.WriteByte('"')
		if err := readStringRest(br, &raw, maxPackumentKey); err != nil {
			return p, err
		}
		var key string
		if err := json.Unmarshal(raw.Bytes(), &key); err != nil {
			return p, err
		}
		if c, err := nextNonSpace(br); err != nil || c != ':' {
			return p, errMalformedPackument
		}
		if err := p.readValue(br, key, maxValue); err != nil {
			return p, err
		}
		c, err = nextNonSpace(br)
		if err != nil {
			return p, err
		}
		switch c {
		case '}':
			return p, nil
		case ',':
			if c, err = nextNonSpace(br); err != nil {
				return p, err
			}
		default:
			return p, errMalformedPackument
		}
	}
}

func (p *packument) readValue(br *bufio.Reader, key string, maxValue int) error {
	switch key {
	case "name":
		var raw bytes.Buffer
		if _, err := walkValue(br, &raw, maxValue); err != nil {
			return err
		}
		return json.Unmarshal(raw.Bytes(), &p.name)
	case "versions":
		var raw bytes.Buffer
		if _, err := walkValue(br, &raw, maxValue); err != nil {
			return err
		}
		var versions map[string]json.RawMessage
		if err := json.Unmarshal(raw.Bytes(), &versions); err != nil {
			return err
		}
		p.versions = slices.Sorted(maps.Keys(versions))
		return nil
	case "_attachments":
		first, err := walkValue(br, nil, 0)
		p.attachments = first == '{'
		return err
	default:
		_, err := walkValue(br, nil, 0)
		return err
	}
}

// walkValue consumes one JSON value from br and returns its first byte. When
// out is non-nil the value's bytes are copied into it, failing once they
// exceed limit. It tracks only strings and nesting, which is enough to find
// where a value ends; captured values are validated by json.Unmarshal.
func walkValue(br *bufio.Reader, out *bytes.Buffer, limit int) (byte, error) {
	first, err := nextNonSpace(br)
	if err != nil {
		return 0, err
	}
	if err := capture(out, limit, first); err != nil {
		return first, err
	}
	switch first {
	case '"':
		return first, readStringRest(br, out, limit)
	case '{', '[':
		for depth := 1; depth > 0; {
			c, err := br.ReadByte()
			if err != nil {
				return first, err
			}
			if err := capture(out, limit, c); err != nil {
				return first, err
			}
			switch c {
			case '"':
				if err := readStringRest(br, out, limit); err != nil {
					return first, err
				}
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
		return first, nil
	default:
		for {
			c, err := br.ReadByte()
			if err != nil {
				return first, err
			}
			if c == ',' || c == '}' || c == ']' || isJSONSpace(c) {
				return first, br.UnreadByte()
			}
			if err := capture(out, limit, c); err != nil {
				return first, err
			}
		}
	}
}

// readStringRest consumes a JSON string up to and including its closing quote;
// the opening quote has already been read.
func readStringRest(br *bufio.Reader, out *bytes.Buffer, limit int) error {
	for escaped := false; ; {
		c, err := br.ReadByte()
		if err != nil {
			return err
		}
		if err := capture(out, limit, c); err != nil {
			return err
		}
		switch {
		case escaped:
			escaped = false
		case c == '\\':
			escaped = true
		case c == '"':
			return nil
		}
	}
}

func capture(out *bytes.Buffer, limit int, c byte) error {
	if out == nil {
		return nil
	}
	if out.Len() >= limit {
		return errMalformedPackument
	}
	return out.WriteByte(c)
}

func nextNonSpace(br *bufio.Reader) (byte, error) {
	for {
		c, err := br.ReadByte()
		if err != nil || !isJSONSpace(c) {
			return c, err
		}
	}
}

func isJSONSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
