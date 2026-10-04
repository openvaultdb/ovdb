package manifest

// Fact is what a document says about one field, in one of three states that a
// caller must tell apart, because the Directory refuses a field that is written
// and unusable where it accepts the same field left out:
//
//   - absent: the key is not written. This is the zero Fact.
//   - present and usable: Present and Valid, with the value in Value.
//   - present and not usable: Present and not Valid, with Value left at its zero
//     value. The key was written with a value of the wrong type, blank, or one
//     that the profile's rule for the field refuses, and a finding says which.
//
// Line is the line where the value starts (0 when absent). A zero value of
// Value is never to be read without asking Usable first.
type Fact[T any] struct {
	Present bool
	Valid   bool
	Value   T
	Line    int
}

// Absent reports whether the key is not written.
func (f Fact[T]) Absent() bool { return !f.Present }

// Usable reports whether the key is written with a value the profile accepts.
func (f Fact[T]) Usable() bool { return f.Present && f.Valid }

// Unusable reports whether the key is written with a value the profile does not accept.
func (f Fact[T]) Unusable() bool { return f.Present && !f.Valid }

// found makes the Fact of a node: absent when n is nil, else present, and usable
// with value when valid.
func found[T any](n *Node, valid bool, value T) Fact[T] {
	if n == nil {
		return Fact[T]{}
	}
	f := Fact[T]{Present: true, Line: n.Line}
	if valid {
		f.Valid, f.Value = true, value
	}
	return f
}
