package cmdtest

import (
	"fmt"
	"reflect"

	"go.uber.org/mock/gomock"
)

type sliceMatcher[T any] struct {
	expected []any
}

func (m sliceMatcher[T]) Matches(x any) bool {
	slice, ok := x.([]T)
	if !ok {
		return false
	}

	if len(slice) != len(m.expected) {
		return false
	}

	for i, exp := range m.expected {
		if matcher, ok := exp.(gomock.Matcher); ok {
			if !matcher.Matches(slice[i]) {
				return false
			}
		} else if !reflect.DeepEqual(slice[i], exp) {
			return false
		}
	}
	return true
}

func (m sliceMatcher[T]) String() string {
	return fmt.Sprintf("matches slice of %s: %v", reflect.TypeFor[T]().String(), m.expected)
}

// SliceMatch returns a matcher that matches a []T slice element-by-element.
// Elements can be values of type T (exact match) or gomock.Matcher instances.
func SliceMatch[T any](elements ...any) gomock.Matcher {
	return sliceMatcher[T]{expected: elements}
}
