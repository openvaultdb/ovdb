package main

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	embedded "github.com/openvaultdb/ovdb/skills"
)

// Every `ovdb …` command and flag an embedded skill tells an agent to run
// exists in this build's command tree (review F2): skills are copied into
// the person's agent folders, so a command they name must work.
func TestSkillCommandsExist(t *testing.T) {
	root := &cobra.Command{Use: "ovdb"}
	addRootCommands(root, "1.0.0")
	command := regexp.MustCompile(`\bovdb( [a-z][^` + "`" + `\n|]*)`)
	checked := 0
	err := fs.WalkDir(embedded.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "SKILL.md") {
			return err
		}
		data, err := fs.ReadFile(embedded.FS, path)
		if err != nil {
			return err
		}
		for _, match := range command.FindAllStringSubmatch(string(data), -1) {
			checked++
			if problem := checkCommand(root, strings.Fields(match[1])); problem != "" {
				t.Errorf("%s: `ovdb%s`: %s", path, match[1], problem)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 20 {
		t.Errorf("only %d commands found in the skills", checked)
	}
}

var commandWord = regexp.MustCompile(`^[a-z][a-z-]*$`)

// checkCommand walks words down the tree: words name subcommands until the
// first argument; every --flag must be defined on the command reached.
func checkCommand(root *cobra.Command, words []string) string {
	cmd := root
	args := false
	for _, word := range words {
		switch {
		case strings.HasPrefix(word, "--"):
			name, _, _ := strings.Cut(strings.TrimPrefix(word, "--"), "=")
			if cmd.Flags().Lookup(name) == nil && cmd.InheritedFlags().Lookup(name) == nil {
				return "no flag --" + name + " on `" + cmd.CommandPath() + "`"
			}
		case !args && commandWord.MatchString(word):
			var next *cobra.Command
			for _, child := range cmd.Commands() {
				if child.Name() == word || child.HasAlias(word) {
					next = child
				}
			}
			if next == nil {
				if cmd == root || !cmd.Runnable() {
					return "no command " + word + " under `" + cmd.CommandPath() + "`"
				}
				args = true
				continue
			}
			cmd = next
		default:
			args = true
		}
	}
	if !cmd.Runnable() {
		return "`" + cmd.CommandPath() + "` is not runnable"
	}
	return ""
}
