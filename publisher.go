package main

import (
	"github.com/spf13/cobra"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/publisher/checkcmd"
	"github.com/openvaultdb/ovdb/internal/publisher/exitcode"
)

// newPublisherCmd is `ovdb publisher`: the command of package checkcmd (which is held to exact test coverage, and so imports neither the copy catalogue nor
// the error envelope) with this binary's catalogue and error envelope. A usage error or a missing tool is the shared envelope error, with exit code 2
// (the publisher commands' exit code for "could not run as asked", through package exitcode); a refused repository is exit code 1, as everywhere.
func newPublisherCmd() *cobra.Command {
	d := checkcmd.Real()
	d.T = uicopy.T
	d.JSON = envelope.Marshal
	d.Usage = func(cmd *cobra.Command, reason string) error {
		return exitcode.Usage(envelope.New(envelope.InvalidArgument, uicopy.T("usage.failed", map[string]string{"command": cmd.CommandPath()})).
			WithReason(reason).
			WithNext(envelope.Next{Label: uicopy.T("next.help", nil), Command: cmd.CommandPath() + " --help"}))
	}
	d.Unrunnable = func(message, reason, next string) error {
		return exitcode.Usage(envelope.New(envelope.DependencyMissing, message).WithReason(reason).WithNext(envelope.Next{Label: next}))
	}
	d.WriteFailed = func(reason string) error {
		return exitcode.Usage(envelope.New(envelope.Internal, uicopy.T("publisher.write.failed", nil)).WithReason(reason).WithNext(envelope.Next{Label: uicopy.T("publisher.write.next", nil)}))
	}
	return checkcmd.NewCmd(d)
}
