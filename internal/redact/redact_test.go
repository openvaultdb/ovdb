package redact

import (
	"bytes"
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"dial postgres://u:s3cret@nohost/db: no such host", "dial postgres://[redacted]@nohost/db: no such host"},
		{"mysql://root@localhost:3306/x", "mysql://[redacted]@localhost:3306/x"},
		{"postgres://u:p@s3cret@host/db", "postgres://[redacted]@host/db"},
		{"postgres://u:a/s3cret@host/db failed", "postgres://[redacted]@host/db failed"},
		{"open u:s3cret@tcp(localhost:3306)/db?parseTime=true", "open [redacted]@tcp(localhost:3306)/db?parseTime=true"},
		{"u:p@s3cret@unix(/tmp/mysql.sock)/db", "[redacted]@unix(/tmp/mysql.sock)/db"},
		{"host=db user=u password=s3cret dbname=x", "host=db user=u password=[redacted] dbname=x"},
		{`api_key: "abc def"`, `api_key: [redacted]`},
		{`{"password":"s3cret","user":"u"}`, `{"password":[redacted],"user":"u"}`},
		{`{"client_secret": "s3cret"}`, `{"client_secret": [redacted]}`},
		{`{"token":tok123}`, `{"token":[redacted]}`},
		{"OVDB_OWNER_TOKEN=tok123;next", "OVDB_OWNER_TOKEN=[redacted];next"},
		{"client_secret='x y'", "client_secret=[redacted]"},
		{"Authorization: Bearer abc.def-ghi", "Authorization: Bearer [redacted]"},
		{"Authorization: Basic dTpzM2NyZXQ=", "Authorization: Basic [redacted]"},
		{"listen tcp 127.0.0.1:6832: bind: address already in use", "listen tcp 127.0.0.1:6832: bind: address already in use"},
		{"http://ovdb.localhost:6832/login", "http://ovdb.localhost:6832/login"},
		{"basic setup done", "basic setup done"},
	} {
		got := String(tc.in)
		if got != tc.want {
			t.Errorf("String(%q)\n got %q\nwant %q", tc.in, got, tc.want)
		}
		for _, secret := range []string{"s3cret", "tok123", "abc.def", "dTpzM2NyZXQ"} {
			if strings.Contains(got, secret) {
				t.Errorf("String(%q) leaks %q", tc.in, secret)
			}
		}
	}
}

func TestWriter(t *testing.T) {
	var buf bytes.Buffer
	line := []byte("http: panic serving: password=s3cret\n")
	n, err := Writer{W: &buf}.Write(line)
	if err != nil || n != len(line) || buf.String() != "http: panic serving: password=[redacted]\n" {
		t.Errorf("Writer = %d, %v, %q", n, err, buf.String())
	}
}
