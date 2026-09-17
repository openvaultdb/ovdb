package runtime

import (
	"context"
	"net"
	"strconv"
	"time"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
)

// DefaultPort spells OVDB on a phone keypad.
const DefaultPort = 6832

// ListenFunc binds a listener; net.Listen satisfies it. Tests inject one to
// simulate an OS that refuses a bind (for example a Windows reserved range).
type ListenFunc func(network, address string) (net.Listener, error)

// Listen binds 127.0.0.1:port and [::1]:port (REQ:loopback-bind). When ::1
// cannot be bound because IPv6 is unavailable the server continues on IPv4;
// when either address is taken it is a conflict, never a silent IPv4-only
// server next to an impostor.
func Listen(port int, listen ListenFunc) ([]net.Listener, *envelope.Error) {
	if listen == nil {
		listen = net.Listen
	}
	v4, err := listen("tcp4", loopbackAddr("127.0.0.1", port))
	if err != nil {
		return nil, bindError(port, err)
	}
	v6, err := listen("tcp6", loopbackAddr("::1", port))
	switch {
	case err == nil:
		return []net.Listener{v4, v6}, nil
	case isIPv6Unavailable(err):
		return []net.Listener{v4}, nil
	default:
		_ = v4.Close()
		return nil, bindError(port, err)
	}
}

// CheckPort reports whether a server could start on port, without keeping
// it. A port with anything answering on either loopback address is in use
// even where the OS would let a more specific bind succeed next to a
// wildcard listener (macOS with SO_REUSEADDR).
func CheckPort(ctx context.Context, port int, listen ListenFunc) *envelope.Error {
	for _, host := range []string{"127.0.0.1", "::1"} {
		dialer := net.Dialer{Timeout: 300 * time.Millisecond}
		if conn, err := dialer.DialContext(ctx, "tcp", loopbackAddr(host, port)); err == nil {
			_ = conn.Close()
			return PortInUse(port)
		}
	}
	listeners, err := Listen(port, listen)
	if err != nil {
		return err
	}
	for _, listener := range listeners {
		_ = listener.Close()
	}
	return nil
}

func bindError(port int, err error) *envelope.Error {
	if isAddrInUse(err) {
		return PortInUse(port)
	}
	return PortUnavailable(port, err)
}

// PortInUse is the port_in_use failure with both fixes, never an automatic
// port change (REQ:deterministic-port-conflict).
func PortInUse(port int) *envelope.Error {
	params := portParams(port)
	return envelope.New(envelope.PortInUse, uicopy.T("server.start.failed", nil)).
		WithReason(uicopy.T("server.port.in_use", params)).
		WithNext(portFixes(params)...)
}

// PortUnavailable is the port_unavailable failure for a bind the OS refuses
// with no listener.
func PortUnavailable(port int, cause error) *envelope.Error {
	params := portParams(port)
	reason := uicopy.T("server.port.unavailable", params)
	if cause != nil {
		reason += " (" + cause.Error() + ")"
	}
	return envelope.New(envelope.PortUnavailable, uicopy.T("server.start.failed", nil)).
		WithReason(reason).
		WithNext(portFixes(params)...)
}

func portParams(port int) map[string]string {
	next := port + 1
	if next > 65535 {
		next = DefaultPort
	}
	return map[string]string{"port": strconv.Itoa(port), "next": strconv.Itoa(next)}
}

func portFixes(params map[string]string) []envelope.Next {
	return []envelope.Next{
		{Label: uicopy.T("next.use_port", params), Command: "ovdb server start --port " + params["next"], Action: "use_port"},
		{Label: uicopy.T("next.keep_port", params), Command: "ovdb config set server.port " + params["next"]},
	}
}

func loopbackAddr(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}
