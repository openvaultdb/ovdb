package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/openvaultdb/openvaultdb-go/pkg/manifest"
)

func newInitCmd() *cobra.Command {
	var id, engine, mode, storagePath, out string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a database manifest",
		Long: `Write a database manifest file for one of the five storage engines:
ingitdb, sqlite, firestore, mysql or postgres. Server engines take their
connection from an environment variable the manifest names, never the file.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// PostgreSQL and MySQL accept only strict mode, and every server
			// engine takes its connection from the environment, never the file.
			serverEngine := engine == "postgres" || engine == "mysql" || engine == "firestore"
			if (engine == "postgres" || engine == "mysql") && !cmd.Flags().Changed("schema-mode") {
				mode = "strict"
			}
			if storagePath == "" && !serverEngine {
				switch engine {
				case "sqlite":
					storagePath = fmt.Sprintf("./data/%s.sqlite", id)
				default:
					storagePath = fmt.Sprintf("./data/%s", id)
				}
			}
			content := fmt.Sprintf(`database:
  id: %s
  schema_mode: %s

storage:
  engine: %s
`, id, mode, engine)
			if storagePath != "" {
				content += "  path: " + storagePath + "\n"
			}
			switch engine {
			case "postgres":
				content += "  postgres:\n    # The connection string stays in this environment variable, never in the file.\n    dsn_env: OVDB_POSTGRES_DSN\n"
			case "mysql":
				content += "  mysql:\n    # The connection string stays in this environment variable, never in the file.\n    dsn_env: OVDB_MYSQL_DSN\n"
			case "firestore":
				content += "  firestore:\n    # Credentials come from Application Default Credentials.\n    project: your-gcp-project\n"
			}
			if mode == "strict" {
				content += `
schemas:
  collections:
    # strict mode requires every collection to be declared before writes, e.g.:
    # contacts:
    #   fields:
    #     title: {type: string, required: true}
    example:
      fields:
        title: {type: string, required: true}
`
			}
			if _, err := manifest.Parse([]byte(content)); err != nil {
				return fmt.Errorf("generated manifest is invalid (bug): %w", err)
			}
			if out == "" {
				out = id + ".yaml"
			}
			if _, err := os.Stat(out); err == nil {
				return fmt.Errorf("%s already exists; refusing to overwrite", out)
			}
			if err := os.WriteFile(out, []byte(content), 0o644); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", out)
			return nil
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "database id (required)")
	cmd.Flags().StringVar(&engine, "engine", "ingitdb", "storage engine: ingitdb | sqlite | firestore | mysql | postgres")
	cmd.Flags().StringVar(&mode, "schema-mode", "schemaless", "schema mode: strict | partial | schemaless")
	cmd.Flags().StringVar(&storagePath, "path", "", "storage path (default derived from id)")
	cmd.Flags().StringVar(&out, "out", "", "output manifest file (default <id>.yaml)")
	_ = cmd.MarkFlagRequired("id")
	cmd.Long += "\n\nEdit the file, then connect it to OVDB:\n  ovdb databases connect --manifest <absolute path>"
	return cmd
}
