package iostreams

import (
	"encoding/json"
	"reflect"
)

// PrintJSON marshals v to JSON and writes it to stdout. If v is a nil slice,
// it converts it to an empty slice so that JSON marshaling produces [] instead
// of null. This addresses the issue where gitlab.ScanAndCollect returns nil for
// empty results, which would otherwise marshal as null instead of [].
//
// Nested slices within the data structure are left as-is to preserve the
// semantic difference between absent fields (null) and empty arrays ([]) in
// the original API response.
func (s *IOStreams) PrintJSON(v any) error {
	// Only normalize if v is a top-level nil slice
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Slice && rv.IsNil() {
		// Create an empty slice of the same type
		v = reflect.MakeSlice(rv.Type(), 0, 0).Interface()
	}

	encoder := json.NewEncoder(s.StdOut)
	return encoder.Encode(v)
}
