package cli

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/client"
	"github.com/openvaultdb/ovdb/internal/datapath"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/setup/dbcontext"
)

// The data commands (database-context-navigation, "Data commands") read and
// write records through the local server's /v1 API, never by opening
// storage themselves. With --json, reads print the /v1 body unchanged,
// writes print {"key":"<absolute escaped path>"}, and failures print the
// /v1 error body unchanged; human output always names the database.

// dataFlags are the flags every data command accepts.
type dataFlags struct {
	db      string
	json    bool
	noStart bool
}

func (f *dataFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.db, "db", "", "the database, instead of the current one")
	cmd.Flags().BoolVar(&f.noStart, "no-start", false, "fail instead of starting the OVDB server")
	jsonFlag(cmd, &f.json)
}

// dataTarget is one data command's resolved database and path.
type dataTarget struct {
	local   *client.Local
	context dbcontext.Context
	path    datapath.Path
	flags   dataFlags
}

func (d dataTarget) op(verb string) client.DataOp {
	return client.DataOp{Verb: verb, Database: d.context.Database, Path: d.path, Suffix: d.suffix()}
}

func (d dataTarget) suffix() string {
	if d.context.Scope == dbcontext.ScopeFlag {
		return " --db " + d.context.Database
	}
	return ""
}

func (a *App) dataTarget(cmd *cobra.Command, flags dataFlags, input string) (dataTarget, error) {
	t, err := a.resolve(0)
	if err != nil {
		return dataTarget{}, err
	}
	local := a.local(cmd, t)
	document, err := a.contextFor(cmd, local, flags.db)
	if err != nil {
		return dataTarget{}, err
	}
	base, _ := datapath.Parse(document.Context.Path)
	path, err := datapath.Resolve(base, input)
	if err != nil {
		return dataTarget{}, err
	}
	return dataTarget{local: local, context: *document.Context, path: path, flags: flags}, nil
}

// requireKind fails with invalid_argument and the command that fits the
// path (REQ:path-kind-mismatch-guidance).
func (d dataTarget) requireKind(want datapath.Kind) error {
	got := d.path.Kind()
	if got == want {
		return nil
	}
	path, suffix := d.path.String(), d.suffix()
	var e *envelope.Error
	switch got {
	case datapath.Root:
		e = envelope.New(envelope.InvalidArgument, uicopy.T("data.kind.root", nil))
	case datapath.Collection:
		e = envelope.New(envelope.InvalidArgument, uicopy.T("data.kind.collection", map[string]string{"path": path}))
	default:
		e = envelope.New(envelope.InvalidArgument, uicopy.T("data.kind.record", map[string]string{"path": path}))
	}
	if want == datapath.Collection {
		sub := path + "/<collection>"
		if got == datapath.Root {
			sub = "/<collection>"
		}
		e = e.WithNext(envelope.Next{Label: uicopy.T("next.add_under", nil), Command: "ovdb add " + sub + " '<json>'" + suffix})
	}
	switch got {
	case datapath.Root:
		e = e.WithNext(envelope.Next{Label: uicopy.T("next.see_collections", nil), Command: "ovdb list /" + suffix})
	case datapath.Collection:
		e = e.WithNext(envelope.Next{Label: uicopy.T("next.see_records", nil), Command: "ovdb list " + path + suffix})
	default:
		e = e.WithNext(envelope.Next{Label: uicopy.T("next.see_record", nil), Command: "ovdb get " + path + suffix})
	}
	return e
}

// written prints a write's result: `todo: added /items/k3f9x2`, or
// {"key":"/items/k3f9x2"} with --json.
func written(cmd *cobra.Command, d dataTarget, line string) {
	if d.flags.json {
		_, _ = cmd.OutOrStdout().Write(envelope.Marshal(struct {
			Key string `json:"key"`
		}{d.path.String()}))
		return
	}
	say(cmd.OutOrStdout(), line)
}

// isEmpty reports whether nothing exists at op.Path yet; a missing record
// is empty, a missing database is an error.
func isEmpty(cmd *cobra.Command, local *client.Local, op client.DataOp, noStart bool) (bool, error) {
	switch op.Path.Kind() {
	case datapath.Root:
		body, err := local.Collections(cmd.Context(), op, noStart)
		if err != nil {
			return false, err
		}
		var parsed client.DatabaseInfo
		return json.Unmarshal(body, &parsed) != nil || len(parsed.Collections) == 0, nil
	case datapath.Collection:
		body, err := local.Query(cmd.Context(), op, 1, noStart)
		if err != nil {
			return false, err
		}
		var parsed client.Records
		return json.Unmarshal(body, &parsed) != nil || len(parsed.Records) == 0, nil
	default:
		_, err := local.Get(cmd.Context(), op, noStart)
		if client.MissingRecord(err) {
			return true, nil
		}
		return false, err
	}
}

// compactJSON is v on one line with keys sorted and no HTML escaping, the
// token-cheap form a listing shows.
func compactJSON(v map[string]any) string {
	if v == nil {
		return "{}"
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(v)
	return strings.TrimSuffix(buf.String(), "\n")
}

func indentJSON(v map[string]any) string {
	if v == nil {
		return "{}"
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(v)
	return strings.TrimSuffix(buf.String(), "\n")
}

func (a *App) listCmd() *cobra.Command {
	var flags dataFlags
	limit := 50
	cmd := &cobra.Command{
		Use:     "list [path]",
		Aliases: []string{"ls"},
		Short:   "List collections, a collection's records, or show a record",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return exactArgs(1)(cmd, args)
			}
			return nil
		},
		RunE: run(func(cmd *cobra.Command, args []string) error {
			input := ""
			if len(args) == 1 {
				input = args[0]
			}
			if limit < 1 {
				return usageError(cmd, uicopy.T("data.limit_invalid", nil))
			}
			d, err := a.dataTarget(cmd, flags, input)
			if err != nil {
				return err
			}
			w, prefix := cmd.OutOrStdout(), d.context.Database+":"+d.path.String()
			switch d.path.Kind() {
			case datapath.Root:
				body, err := d.local.Collections(cmd.Context(), d.op("list"), flags.noStart)
				if err != nil {
					return err
				}
				var parsed client.DatabaseInfo
				if err := json.Unmarshal(body, &parsed); err != nil {
					return err
				}
				printer{cmd: cmd, json: flags.json}.document(body, func(w io.Writer) {
					say(w, prefix)
					if len(parsed.Collections) == 0 {
						say(w, uicopy.T("data.nothing_here", nil))
					}
					for _, name := range parsed.Collections {
						say(w, "  "+datapath.Path{}.Child(name).String())
					}
				})
			case datapath.Collection:
				ask := limit + 1 // one more says whether there are more
				if flags.json {
					ask = limit
				}
				body, err := d.local.Query(cmd.Context(), d.op("list"), ask, flags.noStart)
				if err != nil {
					return err
				}
				var parsed client.Records
				if err := json.Unmarshal(body, &parsed); err != nil {
					return err
				}
				printer{cmd: cmd, json: flags.json}.document(body, func(w io.Writer) {
					say(w, prefix)
					if len(parsed.Records) == 0 {
						say(w, uicopy.T("data.nothing_here", nil))
					}
					for i, rec := range parsed.Records {
						if i == limit {
							say(w, "  "+uicopy.T("data.more", map[string]string{"limit": strconv.Itoa(limit * 2)}))
							break
						}
						say(w, "  "+d.path.Child(client.KeyID(rec.Key)).String()+"  "+compactJSON(rec.Data))
					}
				})
			default:
				body, err := d.local.Get(cmd.Context(), d.op("list"), flags.noStart)
				if client.MissingRecord(err) && !flags.json {
					say(w, prefix)
					say(w, uicopy.T("data.nothing_here", nil))
					return nil
				}
				if err != nil {
					return err
				}
				var parsed client.Record
				if err := json.Unmarshal(body, &parsed); err != nil {
					return err
				}
				printer{cmd: cmd, json: flags.json}.document(body, func(w io.Writer) {
					say(w, prefix)
					say(w, indentJSON(parsed.Data))
					say(w, uicopy.T("data.record_collections_unsupported", nil))
				})
			}
			return nil
		}),
	}
	flags.register(cmd)
	cmd.Flags().IntVar(&limit, "limit", limit, "show at most this many records")
	return cmd
}

func (a *App) getCmd() *cobra.Command {
	var flags dataFlags
	cmd := &cobra.Command{
		Use:   "get <path>",
		Short: "Print one record",
		Args:  exactArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			d, err := a.dataTarget(cmd, flags, args[0])
			if err != nil {
				return err
			}
			if err := d.requireKind(datapath.Record); err != nil {
				return err
			}
			body, err := d.local.Get(cmd.Context(), d.op("get"), flags.noStart)
			if err != nil {
				return err
			}
			var parsed client.Record
			if err := json.Unmarshal(body, &parsed); err != nil {
				return err
			}
			printer{cmd: cmd, json: flags.json}.document(body, func(w io.Writer) {
				say(w, d.context.Database+":"+d.path.String())
				say(w, indentJSON(parsed.Data))
			})
			return nil
		}),
	}
	flags.register(cmd)
	return cmd
}

// readObject parses a record argument: JSON, or "-" for standard input.
func readObject(cmd *cobra.Command, arg string) (map[string]any, error) {
	data := []byte(arg)
	if arg == "-" {
		var err error
		if data, err = io.ReadAll(cmd.InOrStdin()); err != nil {
			return nil, err
		}
	}
	var object map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil || object == nil {
		reason := uicopy.T("data.json_object", nil)
		if err != nil {
			reason += " (" + err.Error() + ")"
		}
		return nil, usageError(cmd, reason)
	}
	return object, nil
}

// parseFields reads --field k=v: v is a JSON literal when valid, a string
// otherwise.
func parseFields(cmd *cobra.Command, fields []string) ([]map[string]any, error) {
	updates := make([]map[string]any, 0, len(fields))
	for _, field := range fields {
		name, raw, ok := strings.Cut(field, "=")
		if !ok || name == "" {
			return nil, usageError(cmd, uicopy.T("data.field_invalid", map[string]string{"field": field}))
		}
		var value any = raw
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.UseNumber()
		var literal any
		if err := decoder.Decode(&literal); err == nil && !decoder.More() {
			value = literal
		}
		updates = append(updates, map[string]any{"fieldName": name, "value": value})
	}
	return updates, nil
}

func (a *App) setCmd() *cobra.Command {
	var flags dataFlags
	var fields []string
	cmd := &cobra.Command{
		Use:   "set <path> [<json> | -]",
		Short: "Create or replace a record, or change named fields with --field",
		Args: func(cmd *cobra.Command, args []string) error {
			switch {
			case len(fields) > 0 && len(args) == 1, len(fields) == 0 && len(args) == 2:
				return nil
			case len(fields) > 0:
				return usageError(cmd, uicopy.T("data.set_field_or_json", nil))
			default:
				return exactArgs(2)(cmd, args)
			}
		},
		RunE: run(func(cmd *cobra.Command, args []string) error {
			request := client.DataRequest{Method: http.MethodPut}
			if len(fields) > 0 {
				updates, err := parseFields(cmd, fields)
				if err != nil {
					return err
				}
				request.Method, request.Body = http.MethodPatch, map[string]any{"updates": updates}
			} else {
				object, err := readObject(cmd, args[1])
				if err != nil {
					return err
				}
				request.Body = map[string]any{"data": object}
			}
			d, err := a.dataTarget(cmd, flags, args[0])
			if err != nil {
				return err
			}
			if err := d.requireKind(datapath.Record); err != nil {
				return err
			}
			request.Op, request.URLPath = d.op("set"), client.RecordURL(d.context.Database, d.path)
			if _, err := d.local.Data(cmd.Context(), request, flags.noStart); err != nil {
				return err
			}
			params := map[string]string{"database": d.context.Database, "path": d.path.String()}
			if len(fields) > 0 {
				written(cmd, d, uicopy.T("data.updated", params))
			} else {
				written(cmd, d, uicopy.T("data.set", params))
			}
			return nil
		}),
	}
	flags.register(cmd)
	cmd.Flags().StringArrayVar(&fields, "field", nil, "change one field, name=value (repeatable); value is JSON when valid, text otherwise")
	return cmd
}

// newID is a short URL-safe id: six lowercase letters and digits.
func newID() string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	for i, b := range buf {
		buf[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(buf)
}

func (a *App) addCmd() *cobra.Command {
	var flags dataFlags
	var id string
	cmd := &cobra.Command{
		Use:   "add <collection path> <json | ->",
		Short: "Add a record with a new id to a collection",
		Args:  exactArgs(2),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			object, err := readObject(cmd, args[1])
			if err != nil {
				return err
			}
			d, err := a.dataTarget(cmd, flags, args[0])
			if err != nil {
				return err
			}
			if err := d.requireKind(datapath.Collection); err != nil {
				return err
			}
			if id == "" {
				d.path = d.path.Child(newID())
			} else {
				// --id is written escaped, like a path segment.
				record, err := datapath.Parse("/c/" + id)
				if err == nil && record.Kind() != datapath.Record {
					err = usageError(cmd, uicopy.T("data.id_invalid", map[string]string{"id": id}))
				}
				if err != nil {
					return err
				}
				d.path = d.path.Child(record.Name())
			}
			request := client.DataRequest{Op: d.op("add"), Method: http.MethodPost, URLPath: client.RecordURL(d.context.Database, d.path), Body: map[string]any{"data": object}}
			if _, err := d.local.Data(cmd.Context(), request, flags.noStart); err != nil {
				return err
			}
			written(cmd, d, uicopy.T("data.added", map[string]string{"database": d.context.Database, "path": d.path.String()}))
			return nil
		}),
	}
	flags.register(cmd)
	cmd.Flags().StringVar(&id, "id", "", "use this id instead of a generated one")
	return cmd
}

func (a *App) deleteCmd() *cobra.Command {
	var flags dataFlags
	cmd := &cobra.Command{
		Use:     "delete <path>",
		Aliases: []string{"rm"},
		Short:   "Delete one record",
		Args:    exactArgs(1),
		RunE: run(func(cmd *cobra.Command, args []string) error {
			d, err := a.dataTarget(cmd, flags, args[0])
			if err != nil {
				return err
			}
			if err := d.requireKind(datapath.Record); err != nil {
				return err
			}
			request := client.DataRequest{Op: d.op("delete"), Method: http.MethodDelete, URLPath: client.RecordURL(d.context.Database, d.path)}
			if _, err := d.local.Data(cmd.Context(), request, flags.noStart); err != nil {
				return err
			}
			written(cmd, d, uicopy.T("data.deleted", map[string]string{"database": d.context.Database, "path": d.path.String()}))
			return nil
		}),
	}
	flags.register(cmd)
	return cmd
}
