package rules

// Relation is how one claimed address stands to another.
type Relation int

const (
	// Apart means the two addresses are different and neither is under the other.
	Apart Relation = iota
	// Same means the addresses are the same once case and trailing slashes are
	// set aside.
	Same
	// Under means the first address sits under the second: the second followed
	// by a slash is a prefix of the first, so the boundary is a path segment
	// (/dbs/chinook2 is not under /dbs/chinook).
	Under
	// Incomparable means an address is not plain ASCII or is longer than
	// MaxClaimLength, so it cannot be compared faithfully. A caller that looks
	// for conflicts treats it as one.
	Incomparable
)

// ClaimedForm is the form of an address that claims are compared in: ASCII
// lower case, trailing slashes removed. It returns false for an address that is
// longer than MaxClaimLength or not ASCII (the JavaScript reference folds
// case by Unicode rules, which this package does not copy, and the addresses
// that reach a comparison have already passed [PublicHTTPSURL]).
func ClaimedForm(address string) (string, bool) {
	if len(address) > MaxClaimLength {
		return "", false
	}
	form := make([]byte, 0, len(address))
	for i := 0; i < len(address); i++ {
		c := address[i]
		switch {
		case c >= 0x80:
			return "", false
		case c >= 'A' && c <= 'Z':
			c += 'a' - 'A'
		}
		form = append(form, c)
	}
	end := len(form)
	for end > 0 && form[end-1] == '/' {
		end--
	}
	return string(form[:end]), true
}

// Compare says how address stands to other, case-insensitively and with
// trailing slashes set aside.
func Compare(address, other string) Relation {
	a, okA := ClaimedForm(address)
	b, okB := ClaimedForm(other)
	switch {
	case !okA || !okB:
		return Incomparable
	case a == b:
		return Same
	case len(a) > len(b) && a[:len(b)] == b && a[len(b)] == '/':
		return Under
	}
	return Apart
}
