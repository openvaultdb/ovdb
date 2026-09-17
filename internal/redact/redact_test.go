package redact

import (
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"dial postgres://u:s3cret@nohost/db: no such host", "dial postgres://[redacted]@nohost/db: no such host"},
		{"mysql://root@localhost:3306/x", "mysql://[redacted]@localhost:3306/x"},
		{"host=db user=u password=s3cret dbname=x", "host=db user=u password=[redacted] dbname=x"},
		{`api_key: "abc def"`, `api_key: [redacted]`},
		{"OVDB_OWNER_TOKEN=tok123;next", "OVDB_OWNER_TOKEN=[redacted];next"},
		{"client_secret='x y'", "client_secret=[redacted]"},
		{"Authorization: Bearer abc.def-ghi", "Authorization: Bearer [redacted]"},
		{"listen tcp 127.0.0.1:6832: bind: address already in use", "listen tcp 127.0.0.1:6832: bind: address already in use"},
		{"http://ovdb.localhost:6832/login", "http://ovdb.localhost:6832/login"},
	} {
		got := String(tc.in)
		if got != tc.want {
			t.Errorf("String(%q)\n got %q\nwant %q", tc.in, got, tc.want)
		}
		for _, secret := range []string{"s3cret", "tok123", "abc.def"} {
			if strings.Contains(got, secret) {
				t.Errorf("String(%q) leaks %q", tc.in, secret)
			}
		}
	}
}
