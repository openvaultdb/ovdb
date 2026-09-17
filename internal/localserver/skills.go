package localserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup/skills"
)

// skillsEnv is the server's own home and environment. Only the web console
// relies on it: the CLI and TUI resolve directories themselves and send them
// (local-server-and-web-console#REQ:client-values-and-mismatch).
func (s *localServer) skillsEnv() (skills.Env, error) {
	getenv := s.opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	return skills.EnvFrom(getenv)
}

func (s *localServer) installedSkills() []skills.Installed {
	env, err := s.skillsEnv()
	if err != nil {
		return nil
	}
	return skills.Status(env)
}

// getSkills is the AI agent skills document (capability 20) for the server's
// home: the harnesses the web console may offer.
func (s *localServer) getSkills(w http.ResponseWriter, _ *http.Request) {
	env, err := s.skillsEnv()
	if err != nil {
		writeError(w, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, skills.Inspect(env))
}

// installSkill installs one skill (capability 21) where the request says,
// within REQ:install-targets-restricted: a console session names harnesses
// the server found, never a directory; the CLI and TUI (the instance secret)
// send the directories they resolved, each checked against a harness layout
// or, for --dir, the home. The request is the person's decision, already
// made in the interface that sent it (REQ:explicit-consent-to-install).
func (s *localServer) installSkill(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var fields map[string]json.RawMessage
	if err == nil {
		err = json.Unmarshal(data, &fields)
	}
	if err != nil {
		envelope.Write(w, envelope.New(envelope.InvalidArgument, uicopy.T("api.bad_json", nil)).WithReason(err.Error()))
		return
	}
	session := credentialOf(r) == credentialSession
	// encoding/json matches field names in any case, so the refusal looks at
	// every spelling: a directory is never taken from a console session, and
	// never as a bare "dir" from anyone (review F1).
	for name := range fields {
		switch strings.ToLower(name) {
		case "dir", "skills_dir":
			writeDirRefused(w)
			return
		case "targets":
			if session {
				writeDirRefused(w)
				return
			}
		}
	}
	var request skills.InstallRequest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if session {
		// A session may send only a skill, harness names and dry_run.
		var sessionRequest struct {
			Skill     string   `json:"skill"`
			Harnesses []string `json:"harnesses"`
			DryRun    bool     `json:"dry_run"`
		}
		err = decoder.Decode(&sessionRequest)
		request = skills.InstallRequest{Skill: sessionRequest.Skill, Harnesses: sessionRequest.Harnesses, DryRun: sessionRequest.DryRun}
	} else {
		err = decoder.Decode(&request)
	}
	if err != nil {
		envelope.Write(w, envelope.New(envelope.InvalidArgument, uicopy.T("api.bad_json", nil)).WithReason(err.Error()))
		return
	}
	env, err := s.skillsEnv()
	if err != nil {
		writeError(w, err)
		return
	}
	d, targets, err := env.Resolve(request)
	if err != nil {
		writeError(w, err)
		return
	}
	for _, t := range targets {
		if session {
			// The console offers only harnesses found in the server's home,
			// at the directory the server itself resolves for them.
			h, known := skills.Harness(t.Harness)
			if found := env.Plan(d, []skills.RequestTarget{t}); !known || !found[0].Detected || t.SkillsDir != h.SkillsDir(env.Home, env.Getenv) {
				envelope.Write(w, envelope.New(envelope.InvalidArgument, uicopy.T("skills.install.failed", map[string]string{"name": env.Describe(d).Name})).
					WithReason(uicopy.T("skills.api.harness_not_found", map[string]string{"harness": skills.HarnessName(t.Harness)})).
					WithNext(envelope.Next{Label: uicopy.T("skills.next.list", nil), Command: "ovdb skills list"}))
				return
			}
			continue
		}
		if err := skills.CheckTarget(d, env.Home, t); err != nil {
			writeError(w, err)
			return
		}
	}
	document, err := skills.Build{Version: s.opts.Record.Version}.Install(r.Context(), env, d, targets, request.DryRun)
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusOK
	if !document.AlreadyUpToDate && !document.DryRun {
		status = http.StatusCreated
	}
	envelope.WriteJSON(w, status, document)
}

func writeDirRefused(w http.ResponseWriter) {
	envelope.Write(w, envelope.New(envelope.InvalidArgument, uicopy.T("skills.api.dir_refused", nil)).
		WithReason(uicopy.T("skills.api.dir_refused_reason", nil)).
		WithNext(envelope.Next{Label: uicopy.T("skills.next.dir_in_terminal", nil), Command: "ovdb skills install <skill> --dir <path>"}))
}
