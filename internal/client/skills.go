package client

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup/skills"
)

// AI agent skills API paths (capabilities 20 and 21).
const (
	SkillsPath        = "/api/local/v1/skills"
	SkillsInstallPath = "/api/local/v1/skills/install"
)

// SkillsEnv is where this client resolves AI agents' skill directories: its
// own home and environment (REQ:client-values-and-mismatch), never the
// server's.
func (l *Local) SkillsEnv() (skills.Env, error) {
	getenv := l.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	return skills.EnvFrom(getenv)
}

func (l *Local) installedSkills() []skills.Installed {
	env, err := l.SkillsEnv()
	if err != nil {
		return nil
	}
	return skills.Status(env)
}

// Skills is the AI agent skills document for this client's environment. It
// only reads skill directories, so it never needs or starts a server.
func (l *Local) Skills(context.Context) ([]byte, error) {
	env, err := l.SkillsEnv()
	if err != nil {
		return nil, err
	}
	return envelope.Marshal(skills.Inspect(env)), nil
}

// SkillPlan is what installing a skill would do, resolved in this client's
// environment and checked, before anything is written: what a person sees
// before deciding (REQ:explicit-consent-to-install).
type SkillPlan struct {
	Skill   skills.Skill
	Request skills.InstallRequest // with Targets resolved
	Targets []skills.Target
}

// PlanSkill resolves request's harnesses (or its --dir target) to
// directories and checks them as the server will, so a refused directory
// fails before a server starts.
func (l *Local) PlanSkill(request skills.InstallRequest) (SkillPlan, error) {
	env, err := l.SkillsEnv()
	if err != nil {
		return SkillPlan{}, err
	}
	for i, t := range request.Targets {
		if abs, err := filepath.Abs(t.SkillsDir); err == nil && t.Harness == "" && t.SkillsDir != "" {
			request.Targets[i].SkillsDir = abs
			if err := skills.CheckUnderHome(mustFind(request.Skill), env.Home, abs); err == nil {
				// A --dir through the home's own link is used by its real path.
				request.Targets[i].SkillsDir = skills.Canonical(abs)
			}
		}
	}
	d, targets, err := env.Resolve(request)
	if err != nil {
		return SkillPlan{}, err
	}
	for _, t := range targets {
		if err := skills.CheckTarget(d, env.Home, t); err != nil {
			return SkillPlan{}, err
		}
	}
	request.Harnesses, request.Targets = nil, targets
	planned := env.Plan(d, targets)
	// Adoption is asked for only when the plan has a folder to take over, so a
	// request that has none is the one every version of the server understands.
	request.Adopt = slices.ContainsFunc(planned, func(t skills.Target) bool { return t.State == skills.StateAdoptable })
	return SkillPlan{Skill: env.Describe(d), Request: request, Targets: planned}, nil
}

// InstallSkill installs the planned skill through the server, starting it
// unless noStart. Only call it after the person chose to install.
func (l *Local) InstallSkill(ctx context.Context, plan SkillPlan, noStart bool) (body []byte, err error) {
	defer func() { l.Telemetry.Record(skills.TelemetryEvents(plan.Request, body, err)...) }()
	c, err := l.Connect(ctx, noStart)
	if err != nil {
		return nil, err
	}
	return l.postInstall(ctx, c, plan.Request)
}

// postInstall sends request to the server c is connected to. A running server
// of a version before adoption existed refuses the field "adopt" as unknown;
// say that the server must be restarted, which is what is wrong.
func (l *Local) postInstall(ctx context.Context, c *Client, request skills.InstallRequest) ([]byte, error) {
	response, err := c.Do(ctx, http.MethodPost, SkillsInstallPath, request)
	if e := envelope.As(err); e != nil && request.Adopt && c.state.Whoami != nil && c.state.Whoami.Version != l.Version &&
		e.Code == envelope.InvalidArgument && strings.Contains(e.Reason, `"adopt"`) {
		mismatch := VersionMismatch(c.state.Whoami.Version, l.Version)
		return envelope.MarshalError(mismatch), mismatch
	}
	return response.Body, err
}

// DryRunSkill reports what installing the planned skill would change,
// writing nothing and starting no server.
func (l *Local) DryRunSkill(ctx context.Context, plan SkillPlan) ([]byte, error) {
	env, err := l.SkillsEnv()
	if err != nil {
		return nil, err
	}
	d, _ := skills.Find(plan.Skill.ID)
	document, err := skills.Build{Version: l.Version}.Install(ctx, env, d, plan.Request.Targets, true, false, plan.Request.Adopt)
	if err != nil {
		return nil, err
	}
	return envelope.Marshal(document), nil
}

func mustFind(id string) skills.Definition {
	d, _ := skills.Find(id)
	return d
}
