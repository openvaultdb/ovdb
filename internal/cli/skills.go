package cli

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/client"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup/skills"
)

// skillsCmd is `ovdb skills list|install` (capabilities 20 and 21,
// spec/features/ai-agent-skills).
func (a *App) skillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "See and install the AI agent skills for OpenVaultDB",
		Args:  noArgs,
	}
	cmd.AddCommand(a.skillsListCmd(), a.skillsInstallCmd())
	for _, sub := range cmd.Commands() {
		sub.SetFlagErrorFunc(flagError)
	}
	return cmd
}

func (a *App) skillsListCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the skills and where each AI agent keeps them",
		Args:  noArgs,
		RunE: run(func(cmd *cobra.Command, _ []string) error {
			t, err := a.resolve(0)
			if err != nil {
				return err
			}
			body, err := a.local(cmd, t).Skills(cmd.Context())
			if err != nil {
				return err
			}
			var document skills.Document
			if err := json.Unmarshal(body, &document); err != nil {
				return err
			}
			printer{cmd: cmd, json: jsonOut}.document(body, func(w io.Writer) {
				say(w, uicopy.T("skills.title", nil))
				for _, skill := range document.Skills {
					say(w, "")
					say(w, skill.Name+" ("+skill.ID+")")
					say(w, "  "+skill.Purpose)
					for _, target := range skill.Targets {
						say(w, "  "+targetLine(target, true))
					}
					say(w, "  "+uicopy.T("skills.list.install", map[string]string{"command": skill.Command}))
				}
			})
			return nil
		}),
	}
	jsonFlag(cmd, &jsonOut)
	return cmd
}

// targetLine is "Claude Code  installed  /h/.claude/skills/openvaultdb".
func targetLine(target skills.Target, withState bool) string {
	name := target.Name
	if target.Harness == "" {
		name = uicopy.T("skills.target.folder", nil)
	}
	line := padRight(name, 14)
	if withState {
		state := uicopy.T("skills.state.not_installed", nil)
		switch {
		case target.Installed:
			state = uicopy.T("skills.state."+target.State, nil)
		case !target.Detected:
			state = uicopy.T("skills.state.not_found", nil)
		}
		line += padRight(state, 22)
	}
	return line + target.Dir
}

func padRight(text string, width int) string {
	if n := len([]rune(text)); n < width {
		return text + strings.Repeat(" ", width-n)
	}
	return text + "  "
}

func (a *App) skillsInstallCmd() *cobra.Command {
	var yes, dryRun, noStart, jsonOut bool
	var harnesses []string
	var dir string
	cmd := &cobra.Command{
		Use:   "install <openvaultdb|todo-demo>",
		Short: "Install one skill for your AI agents (asks first)",
		Long: "Install one skill for your AI agents. Without --harness or --dir it goes to every AI agent found on\n" +
			"this computer (Claude Code when none is). Installing changes your AI agent's setup, so it asks\n" +
			"first; without a terminal it needs --yes, which an AI agent may pass only after you said yes.",
		Args: exactArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			if dir != "" && len(harnesses) > 0 {
				return usageError(cmd, "--dir and --harness cannot be used together")
			}
			t, err := a.resolve(0)
			if err != nil {
				return err
			}
			local := a.local(cmd, t)
			request := skills.InstallRequest{Skill: args[0], Harnesses: harnesses, DryRun: dryRun}
			if dir != "" {
				request.Targets = []skills.RequestTarget{{SkillsDir: dir}}
			}
			plan, err := local.PlanSkill(request)
			if err != nil {
				return err
			}
			var body []byte
			switch {
			case dryRun:
				body, err = local.DryRunSkill(cmd.Context(), plan)
			default:
				if !yes {
					confirmed, err := a.confirmSkill(cmd, plan, args[0], harnesses, dir, jsonOut)
					if err != nil || !confirmed {
						return err
					}
				}
				body, err = local.InstallSkill(cmd.Context(), plan, noStart)
			}
			if err != nil {
				return err
			}
			var document skills.InstallDocument
			if err := json.Unmarshal(body, &document); err != nil {
				return err
			}
			printer{cmd: cmd, json: jsonOut}.document(body, func(w io.Writer) {
				title := "skills.installed.title"
				switch {
				case document.DryRun:
					title = "skills.dry_run.title"
				case document.AlreadyUpToDate:
					title = "skills.up_to_date.title"
				}
				say(w, uicopy.T(title, map[string]string{"name": document.Name}))
				say(w, "")
				for _, outcome := range document.Outcomes {
					say(w, "  "+targetLine(outcome.Target, false)+"  ("+uicopy.T("skills.result."+outcome.Result, nil)+")")
				}
				if len(document.Next) > 0 {
					say(w, "")
					say(w, uicopy.T("home.what_next", nil))
					writeNext(w, document.Next)
				}
			})
			return nil
		}),
	}
	cmd.Flags().StringArrayVar(&harnesses, "harness", nil, "AI agent to install for: claude, cursor, codex, … or all (repeatable)")
	cmd.Flags().StringVar(&dir, "dir", "", "install into this skills folder instead (must be inside your home folder)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without writing")
	cmd.Flags().BoolVar(&yes, "yes", false, "install without asking; an AI agent passes it only after the person said yes")
	cmd.Flags().BoolVar(&noStart, "no-start", false, "fail instead of starting the OVDB server")
	jsonFlag(cmd, &jsonOut)
	return cmd
}

// confirmSkill shows the skill's purpose and exact directories and asks in a
// terminal; anywhere else it fails with confirmation_required naming --yes,
// having written nothing (ai-agent-skills#REQ:explicit-consent-to-install).
func (a *App) confirmSkill(cmd *cobra.Command, plan client.SkillPlan, id string, harnesses []string, dir string, jsonOut bool) (bool, error) {
	var dirs []string
	for _, target := range plan.Targets {
		dirs = append(dirs, target.Dir)
	}
	command := "ovdb skills install " + id
	for _, harness := range harnesses {
		command += " --harness " + harness
	}
	if dir != "" {
		command += " --dir " + quoteArg(dir)
	}
	needed := envelope.New(envelope.ConfirmationRequired, uicopy.T("skills.install.failed", map[string]string{"name": plan.Skill.Name})).
		WithReason(uicopy.T("skills.install.confirm_needed", map[string]string{"dirs": strings.Join(dirs, ", ")})).
		WithNext(envelope.Next{Label: uicopy.T("skills.install.confirm_flag", nil), Command: command + " --yes"})
	in, isFile := cmd.InOrStdin().(*os.File)
	if jsonOut || a.getenv(EnvNonInteractive) == "1" || !isFile || !a.isTerminal(in.Fd()) {
		return false, needed
	}
	w := cmd.ErrOrStderr()
	say(w, uicopy.T("skills.consent.question", map[string]string{"name": plan.Skill.Name}))
	say(w, "")
	say(w, plan.Skill.Purpose)
	say(w, plan.Skill.Example)
	say(w, "")
	say(w, uicopy.T("skills.consent.install_for", nil))
	for _, target := range plan.Targets {
		say(w, "  "+targetLine(target, false))
	}
	_, _ = io.WriteString(w, uicopy.T("skills.install.confirm", nil))
	answer, _ := bufio.NewReader(in).ReadString('\n')
	if answer = strings.ToLower(strings.TrimSpace(answer)); answer == "y" || answer == "yes" {
		return true, nil
	}
	say(cmd.OutOrStdout(), uicopy.T("skills.install.cancelled", nil))
	return false, nil
}

func quoteArg(arg string) string {
	if strings.ContainsAny(arg, " \t'\"") {
		return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
	}
	return arg
}
