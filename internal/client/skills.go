package client

import (
	"context"
	"net/http"
	"os"
	"path/filepath"

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
	return SkillPlan{Skill: env.Describe(d), Request: request, Targets: env.Plan(d, targets)}, nil
}

// InstallSkill installs the planned skill through the server, starting it
// unless noStart. Only call it after the person chose to install.
func (l *Local) InstallSkill(ctx context.Context, plan SkillPlan, noStart bool) ([]byte, error) {
	c, err := l.Connect(ctx, noStart)
	if err != nil {
		return nil, err
	}
	response, err := c.Do(ctx, http.MethodPost, SkillsInstallPath, plan.Request)
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
	document, err := skills.Build{Version: l.Version}.Install(ctx, env, d, plan.Request.Targets, true)
	if err != nil {
		return nil, err
	}
	return envelope.Marshal(document), nil
}
