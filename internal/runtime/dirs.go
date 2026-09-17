package runtime

import (
	"errors"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
)

// PrepareDirs creates the OVDB home and runtime directory owner-only when
// they are missing. An existing directory is never changed: when it is
// accessible to other users, a warning with the fix is returned, and for the
// runtime directory — where the instance secret lives — the result is also a
// forbidden error, so no secret is ever written there.
//
// failure is the message of the resulting error ("Couldn't start the OVDB
// server", "Couldn't change the setting").
//
// See REQ:owner-only-state.
func PrepareDirs(dirs paths.Dirs, failure string) (warnings []string, err *envelope.Error) {
	for _, dir := range []struct {
		path    string
		secrets bool
	}{{dirs.Home, false}, {dirs.Runtime, true}} {
		ensureErr := paths.EnsurePrivateDir(dir.path)
		switch {
		case ensureErr == nil:
		case errors.Is(ensureErr, paths.ErrNotPrivate):
			warnings = append(warnings, uicopy.T("warning.dir_not_private", map[string]string{
				"dir": dir.path, "fix": paths.PrivacyFix(dir.path),
			}))
			if dir.secrets && err == nil {
				err = envelope.New(envelope.Forbidden, failure).
					WithReason(uicopy.T("runtime.not_private", map[string]string{"dir": dir.path})).
					WithNext(envelope.Next{Label: uicopy.T("next.make_dir_private", nil), Command: paths.PrivacyFix(dir.path)})
			}
		default:
			if err == nil {
				err = envelope.New(envelope.Internal, failure).
					WithReason(ensureErr.Error())
			}
		}
	}
	return warnings, err
}
