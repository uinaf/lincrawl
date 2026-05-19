// Package cli wires the lincrawl Kong command tree, JSON output, and exit
// codes. It owns argument parsing and the thin orchestration that connects
// config → store → syncer.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/alecthomas/kong"

	"github.com/uinaf/lincrawl/internal/archive"
	"github.com/uinaf/lincrawl/internal/buildinfo"
	"github.com/uinaf/lincrawl/internal/config"
	"github.com/uinaf/lincrawl/internal/guard"
	"github.com/uinaf/lincrawl/internal/linear"
	"github.com/uinaf/lincrawl/internal/lock"
	"github.com/uinaf/lincrawl/internal/store"
	"github.com/uinaf/lincrawl/internal/syncer"
	"github.com/uinaf/lincrawl/internal/tenantstore"
)

type rootCmd struct {
	Doctor    doctorCmd    `cmd:"" help:"Check local configuration."`
	Describe  describeCmd  `cmd:"" help:"Print machine-readable command schemas."`
	Status    statusCmd    `cmd:"" help:"Print local archive status."`
	Sync      syncCmd      `cmd:"" help:"Sync issues from fixtures or Linear."`
	Search    searchCmd    `cmd:"" help:"Search the local archive."`
	Show      showCmd      `cmd:"" help:"Show one local issue by id or identifier."`
	Query     queryCmd     `cmd:"" help:"Run a raw Linear GraphQL query."`
	Export    exportCmd    `cmd:"" help:"Export the local archive as canonical JSONL."`
	Archive   archiveCmd   `cmd:"" help:"Write an encrypted snapshot from a fixture or the local store."`
	Publish   publishCmd   `cmd:"" help:"Publish the local archive as an encrypted snapshot."`
	Import    importCmd    `cmd:"" help:"Import an encrypted snapshot into the local archive."`
	Store     storeCmd     `cmd:"" help:"Inspect or verify a tenant-controlled snapshot store."`
	Subscribe subscribeCmd `cmd:"" help:"Import every snapshot in a verified tenant store."`
	Guard     guardCmd     `cmd:"" help:"Scan the working tree for tenant data leaks."`
	Version   versionCmd   `cmd:"" help:"Print version information."`
}

type archiveCmd struct {
	Fixture   string `help:"Read records from a fixture directory instead of the local store."`
	Recipient string `help:"Age recipient (age1... or ssh-... public key) for encryption. Defaults to LINCRAWL_AGE_RECIPIENT."`
	Out       string `help:"Output path (sandboxed under CWD). Must end with .jsonl.zst.age."`
	DryRun    bool   `help:"Print the would-write plan without touching disk." name:"dry-run"`
	JSON      bool   `help:"Emit JSON." default:"true" negatable:""`
}

type publishCmd struct {
	Recipient string `help:"Age recipient (age1... or ssh-... public key). Defaults to LINCRAWL_AGE_RECIPIENT."`
	Out       string `help:"Output path (sandboxed under CWD). Must end with .jsonl.zst.age."`
	DryRun    bool   `help:"Print the would-write plan without touching disk." name:"dry-run"`
	JSON      bool   `help:"Emit JSON." default:"true" negatable:""`
}

type importCmd struct {
	In       string `help:"Path to an encrypted snapshot (*.jsonl.zst.age)."`
	Identity string `help:"Age identity (PEM SSH private key or age-secret-key). Defaults to LINCRAWL_AGE_IDENTITY / LINCRAWL_AGE_IDENTITY_FILE."`
	DryRun   bool   `help:"Decode + count without writing to SQLite." name:"dry-run"`
	JSON     bool   `help:"Emit JSON." default:"true" negatable:""`
}

type storeCmd struct {
	Verify storeVerifyCmd `cmd:"" help:"Verify a tenant snapshot store's manifest and tree."`
}

type storeVerifyCmd struct {
	Path string `arg:"" help:"Path to the tenant snapshot store root."`
	JSON bool   `help:"Emit JSON." default:"true" negatable:""`
}

type subscribeCmd struct {
	Path     string `arg:"" help:"Path to the tenant snapshot store root."`
	Identity string `help:"Age identity. Defaults to LINCRAWL_AGE_IDENTITY / LINCRAWL_AGE_IDENTITY_FILE."`
	DryRun   bool   `help:"Verify and report the import plan without writing." name:"dry-run"`
	JSON     bool   `help:"Emit JSON." default:"true" negatable:""`
}

type guardCmd struct {
	Root string `help:"Working tree root to scan." default:"."`
	JSON bool   `help:"Emit JSON." default:"true" negatable:""`
}

func (c *guardCmd) Run(cc commandContext) error {
	res, err := guard.Run(c.Root)
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	if !res.OK {
		if c.JSON {
			_ = writeJSON(cc.stderr, res)
		} else {
			for _, f := range res.Findings {
				fmt.Fprintf(cc.stderr, "guard: %s — %s\n", f.Path, f.Reason)
			}
		}
		return &CLIError{Code: "validation", ExitVal: ExitValidation, Message: fmt.Sprintf("guard: %d findings", len(res.Findings))}
	}
	if c.JSON {
		return writeJSON(cc.stdout, res)
	}
	fmt.Fprintf(cc.stdout, "guard: ok (%d files scanned)\n", res.Scanned)
	return nil
}

// Run parses args and dispatches to the chosen subcommand. The returned int
// is intended to be passed to os.Exit; 0 on success.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var cli rootCmd
	parser, err := kong.New(&cli,
		kong.Name("lincrawl"),
		kong.Description("Local-first Linear archive CLI."),
		kong.Writers(stdout, stderr),
		kong.Exit(func(code int) {
			panic(kongExit(code))
		}),
	)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	kctx, parseErr := func() (kctx *kong.Context, err error) {
		defer func() {
			if r := recover(); r != nil {
				if exit, ok := r.(kongExit); ok {
					err = errParserExit{code: int(exit)}
					return
				}
				panic(r)
			}
		}()
		kctx, err = parser.Parse(args)
		return
	}()
	if parseErr != nil {
		var ex errParserExit
		if errors.As(parseErr, &ex) {
			return ex.code
		}
		return writeError(stderr, usageErr(parseErr.Error()), errIsJSONFromArgs(args))
	}
	// Kong's success exits (e.g. --help, --version handled by Kong) trip the
	// kong.Exit override; the recover swallows them with parseErr already
	// consumed. kctx may be nil — short-circuit before dispatching.
	if kctx == nil {
		return 0
	}

	runCtx := commandContext{Context: ctx, stdout: stdout, stderr: stderr}
	if err := kctx.Run(runCtx); err != nil {
		return writeError(stderr, err, errIsJSONFromArgs(args))
	}
	return ExitOK
}

type (
	kongExit      int
	errParserExit struct{ code int }
)

func (e errParserExit) Error() string { return fmt.Sprintf("parser exit %d", e.code) }

type commandContext struct {
	context.Context
	stdout io.Writer
	stderr io.Writer
}

type versionCmd struct {
	JSON bool `help:"Emit JSON." default:"true" negatable:""`
}

func (c *versionCmd) Run(cc commandContext) error {
	if c.JSON {
		return writeJSON(cc.stdout, buildinfo.Current())
	}
	info := buildinfo.Current()
	fmt.Fprintf(cc.stdout, "lincrawl %s (%s, %s)\n", info.Version, info.Commit, info.Date)
	return nil
}

// describeCmd emits machine-readable schema for every command so agents can
// introspect the surface without reading help text.
type describeCmd struct {
	Command string `arg:"" help:"Single command to describe; omit to dump every command." optional:""`
	JSON    bool   `help:"Emit JSON." default:"true" negatable:""`
}

type flagSpec struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Default  string   `json:"default,omitempty"`
	Help     string   `json:"help,omitempty"`
	Enum     []string `json:"enum,omitempty"`
}

type argSpec struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
	Help     string `json:"help,omitempty"`
}

type commandDescription struct {
	Name              string     `json:"name"`
	Help              string     `json:"help"`
	Mutates           bool       `json:"mutates"`
	Args              []argSpec  `json:"args"`
	Flags             []flagSpec `json:"flags"`
	MutuallyExclusive [][]string `json:"mutually_exclusive,omitempty"`
	Example           string     `json:"example,omitempty"`
	Examples          []string   `json:"examples,omitempty"`
	Notes             []string   `json:"notes,omitempty"`
}

var mutuallyExclusiveByCommand = map[string][][]string{
	"sync":  {{"--fixture", "--stdin", "--entities", "--updated-since", "--resume", "--issue"}},
	"query": {{"--graphql", "--graphql-file"}},
}

// mutatingCommands list commands that write to the store, network, or
// filesystem. Agents branch on `mutates: true` to decide whether to
// preflight with `--dry-run`.
var mutatingCommands = map[string]bool{
	"sync":         true,
	"export":       true,
	"archive":      true,
	"publish":      true,
	"import":       true,
	"subscribe":    true,
	"store verify": false, // read-only; explicit so agents do not preflight
}

var commandNotes = map[string][]string{
	"sync": {
		"Live modes (--entities, --updated-since, --resume, --issue) require LINEAR_API_KEY.",
		"--max-issues bounds blast radius and is honored per page.",
		"--ndjson streams one issue per line; cursor only advances for pages fully drained to the consumer.",
		"Tail sync applies a 60s overlap window so updates inside the same second never escape between runs.",
	},
	"search": {
		"User-supplied <query> is quoted as a literal FTS5 phrase. Use --raw to opt into FTS5 syntax.",
		"--fields trims response payload; unknown fields produce a validation error listing known keys.",
	},
	"show":  {"Resolves either a Linear UUID or the TEAM-N identifier (case-insensitive)."},
	"query": {"Returns the raw GraphQL data envelope. Does not write to the store."},
	"export": {
		"NDJSON envelopes: {\"kind\":\"team|state|user|label|project|issue\",\"item\":{...}}.",
		"--out is sandboxed under CWD with symlink resolution; --out - writes to stdout.",
		"Round-trips losslessly via `sync --stdin`.",
	},
	"archive": {
		"Writes records from a fixture directory only; use `publish` to archive the local store.",
		"Recipient is read from --recipient or LINCRAWL_AGE_RECIPIENT (age1... or ssh-... public key).",
		"--out is sandboxed under CWD and must end with .jsonl.zst.age.",
	},
	"publish": {
		"Encrypts the entire local archive (teams, states, users, labels, projects, issues + comments).",
		"Reads the store inside a single transaction for a consistent snapshot.",
		"Recipient is read from --recipient or LINCRAWL_AGE_RECIPIENT.",
	},
	"import": {
		"Decrypts a single .jsonl.zst.age snapshot and ingests it via the same idempotent upsert path as sync.",
		"--in accepts an absolute path so operators can import from a tenant store mount; --graphql-file is sandboxed.",
		"Identity is read from --identity, LINCRAWL_AGE_IDENTITY, or LINCRAWL_AGE_IDENTITY_FILE.",
	},
	"store verify": {
		"Reads manifest.json and walks the store tree.",
		"Enforces the canonical artifacts/snapshots/{full,delta}/YYYY/MM/ layout and *.jsonl.zst.age extension.",
		"Rejects plaintext archives, runtime state, symlinks, and forbidden directories (logs/, reports/, screenshots/, transcripts/).",
	},
	"subscribe": {
		"Runs `store verify` then ingests each listed snapshot in manifest order under a write lock.",
		"On partial failure the error message names the snapshot path that failed; already-ingested snapshots stay in the local archive.",
		"Identity is read from --identity, LINCRAWL_AGE_IDENTITY, or LINCRAWL_AGE_IDENTITY_FILE.",
	},
	"guard": {"Honors .gitignore in git checkouts. Scans every tracked-style file under 2 MiB."},
}

var commandExtraExamples = map[string][]string{
	"sync": {
		"lincrawl sync --fixture testdata/synthetic --json",
		"lincrawl sync --entities --json",
		"lincrawl sync --updated-since 24h --max-issues 200 --json",
		"lincrawl sync --resume --max-issues 1000 --json",
		"lincrawl sync --issue LIN-42 --dry-run --json",
		"lincrawl sync --updated-since 24h --ndjson | jq -c '{identifier,updated_at}'",
	},
	"search": {
		`lincrawl search "billing" --fields identifier,title,snippet --json`,
		`lincrawl search "billing" --ndjson | head -20`,
		`lincrawl search 'identifier:LIN-*' --raw --json`,
	},
}

type describeResult struct {
	SchemaVersion string               `json:"schema_version"`
	Tool          string               `json:"tool"`
	Version       string               `json:"version"`
	ExitCodes     map[string]int       `json:"exit_codes"`
	FieldMasks    map[string][]string  `json:"field_masks"`
	Commands      []commandDescription `json:"commands"`
}

const describeSchemaVersion = "lincrawl.cli.v1"

var commandExamples = map[string]string{
	"doctor":       "lincrawl doctor --offline --json",
	"describe":     "lincrawl describe --json",
	"status":       "lincrawl status --json --fields counts",
	"sync":         "lincrawl sync --updated-since 24h --max-issues 200 --json",
	"search":       `lincrawl search "ingest" --fields identifier,title --json`,
	"show":         "lincrawl show LIN-1 --fields identifier,title,labels --json",
	"query":        `lincrawl query --graphql 'query { viewer { id name } }' --json`,
	"export":       "lincrawl export --out ./snapshots/lincrawl.jsonl --json",
	"archive":      "lincrawl archive --fixture testdata/synthetic --recipient $LINCRAWL_AGE_RECIPIENT --out ./out.jsonl.zst.age --json",
	"publish":      "lincrawl publish --recipient $LINCRAWL_AGE_RECIPIENT --out ./snapshots/lincrawl-full.jsonl.zst.age --json",
	"import":       "lincrawl import --in ./snapshots/lincrawl-full.jsonl.zst.age --identity-file ~/.lincrawl-identity --json",
	"store verify": "lincrawl store verify ./tenant-store --json",
	"subscribe":    "lincrawl subscribe ./tenant-store --identity-file ~/.lincrawl-identity --json",
	"guard":        "lincrawl guard --json",
	"version":      "lincrawl version --json",
}

func (c *describeCmd) Run(cc commandContext) error {
	var root rootCmd
	parser, err := kong.New(&root, kong.Name("lincrawl"), kong.Exit(func(int) {}))
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	model := parser.Model
	result := describeResult{
		SchemaVersion: describeSchemaVersion,
		Tool:          "lincrawl",
		Version:       buildinfo.Current().Version,
		ExitCodes: map[string]int{
			"ok":         ExitOK,
			"internal":   ExitInternal,
			"usage":      ExitUsage,
			"not_found":  ExitNotFound,
			"validation": ExitValidation,
			"config":     ExitConfig,
		},
		FieldMasks: map[string][]string{
			"show":   {"id", "identifier", "title", "description", "team_id", "project_id", "state_id", "assignee_id", "creator_id", "priority", "created_at", "updated_at", "team_key", "state_name", "state_type", "labels", "comments"},
			"search": {"id", "identifier", "title", "team_key", "state_name", "state_type", "updated_at", "snippet", "score"},
			"status": {"home", "database_path", "exists", "counts"},
		},
	}
	walkCommands(model.Node, "", &result.Commands)
	if c.Command != "" {
		filtered := result.Commands[:0]
		for _, cmd := range result.Commands {
			if cmd.Name == c.Command {
				filtered = append(filtered, cmd)
			}
		}
		if len(filtered) == 0 {
			return notFoundErr(fmt.Sprintf("describe: no such command %q", c.Command))
		}
		result.Commands = filtered
	}
	if c.JSON {
		return writeJSON(cc.stdout, result)
	}
	for _, cmd := range result.Commands {
		fmt.Fprintf(cc.stdout, "%-10s %s\n", cmd.Name, cmd.Help)
	}
	return nil
}

// walkCommands recursively visits the Kong model and emits one
// commandDescription per command leaf. Group commands (those with
// child subcommands and no Run method) are skipped; the children show
// up as multi-word names like "store verify".
func walkCommands(node *kong.Node, prefix string, out *[]commandDescription) {
	for _, child := range node.Children {
		if child.Type != kong.CommandNode {
			continue
		}
		name := child.Name
		if prefix != "" {
			name = prefix + " " + child.Name
		}
		hasSubcommands := false
		for _, gc := range child.Children {
			if gc.Type == kong.CommandNode {
				hasSubcommands = true
				break
			}
		}
		if hasSubcommands {
			walkCommands(child, name, out)
			continue
		}
		cmd := commandDescription{
			Name:     name,
			Help:     child.Help,
			Mutates:  mutatingCommands[name],
			Args:     []argSpec{},
			Flags:    []flagSpec{},
			Example:  commandExamples[name],
			Examples: commandExtraExamples[name],
			Notes:    commandNotes[name],
		}
		for _, pos := range child.Positional {
			cmd.Args = append(cmd.Args, argSpec{
				Name:     pos.Name,
				Type:     pos.Target.Type().String(),
				Required: pos.Required,
				Help:     pos.Help,
			})
		}
		for _, f := range child.Flags {
			if f.Name == "help" {
				continue
			}
			spec := flagSpec{
				Name:     "--" + f.Name,
				Type:     f.Target.Type().String(),
				Required: f.Required,
				Default:  f.Default,
				Help:     f.Help,
			}
			if f.Enum != "" {
				spec.Enum = splitCSV(f.Enum)
			}
			cmd.Flags = append(cmd.Flags, spec)
		}
		if groups, ok := mutuallyExclusiveByCommand[name]; ok {
			cmd.MutuallyExclusive = groups
		}
		*out = append(*out, cmd)
	}
}

func splitCSV(in string) []string {
	parts := strings.Split(in, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

type doctorCmd struct {
	Offline bool `help:"Skip the live LINEAR_API_KEY connectivity probe."`
	JSON    bool `help:"Emit JSON." default:"true" negatable:""`
}

type doctorResult struct {
	OK             bool     `json:"ok"`
	Offline        bool     `json:"offline"`
	Home           string   `json:"home"`
	DatabasePath   string   `json:"database_path"`
	ConfigDir      string   `json:"config_dir"`
	DotEnvPath     string   `json:"dotenv_path"`
	DotEnvLoaded   bool     `json:"dotenv_loaded"`
	LinearAPIBase  string   `json:"linear_api_base"`
	LinearAPIToken string   `json:"linear_api_token"`
	AgeRecipient   string   `json:"age_recipient"`
	AgeIdentity    string   `json:"age_identity"`
	Notes          []string `json:"notes,omitempty"`
}

func (c *doctorCmd) Run(cc commandContext) error {
	rt, err := config.LoadRuntime()
	if err != nil {
		return wrapErr(err, "config", ExitConfig)
	}
	if err := config.EnsureDirs(rt); err != nil {
		return wrapErr(err, "config", ExitConfig)
	}
	res := doctorResult{
		OK:             true,
		Offline:        c.Offline,
		Home:           rt.Home,
		DatabasePath:   rt.DatabasePath,
		ConfigDir:      rt.ConfigDir,
		DotEnvPath:     rt.DotEnvPath,
		DotEnvLoaded:   rt.LoadedDotEnv,
		LinearAPIBase:  rt.LinearAPIBase,
		LinearAPIToken: config.Redact(rt.LinearAPIKeySet),
		AgeRecipient:   config.Redact(rt.AgeRecipientSet),
		AgeIdentity:    config.Redact(rt.AgeIdentitySet),
	}
	if !c.Offline && !rt.LinearAPIKeySet {
		res.OK = false
		res.Notes = append(res.Notes, "LINEAR_API_KEY is unset; live sync (--entities, --updated-since, --issue, --resume) will fail.")
	}
	if c.JSON {
		return writeJSON(cc.stdout, res)
	}
	fmt.Fprintf(cc.stdout, "lincrawl doctor: home=%s db=%s linear_token=%s\n",
		res.Home, res.DatabasePath, res.LinearAPIToken)
	return nil
}

type statusCmd struct {
	Fields string `help:"Comma-separated field whitelist for the JSON output."`
	JSON   bool   `help:"Emit JSON." default:"true" negatable:""`
}

type statusResult struct {
	Home         string        `json:"home"`
	DatabasePath string        `json:"database_path"`
	Exists       bool          `json:"exists"`
	Counts       store.Counts  `json:"counts"`
	Resume       *resumeStatus `json:"resume,omitempty"`
}

type resumeStatus struct {
	State           string `json:"state"`
	HighWaterMark   string `json:"high_water_mark"`
	ResumeAvailable bool   `json:"resume_available"`
}

func (c *statusCmd) Run(cc commandContext) error {
	rt, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	res := statusResult{Home: rt.Home, DatabasePath: rt.DatabasePath}
	if _, err := os.Stat(rt.DatabasePath); err == nil {
		res.Exists = true
		s, err := store.OpenReadOnly(rt.DatabasePath)
		if err != nil {
			return err
		}
		defer s.Close()
		counts, err := s.Counts()
		if err != nil {
			return err
		}
		res.Counts = counts
		if cur, err := s.LoadCursor("issues.tail"); err == nil && cur.HighWaterMark != "" {
			res.Resume = &resumeStatus{
				State:           "active",
				HighWaterMark:   cur.HighWaterMark,
				ResumeAvailable: true,
			}
		}
	}
	if c.JSON {
		return writeProjected(cc.stdout, res, c.Fields)
	}
	fmt.Fprintf(cc.stdout, "status: db=%s exists=%t teams=%d issues=%d comments=%d\n",
		res.DatabasePath, res.Exists, res.Counts.Teams, res.Counts.Issues, res.Counts.Comments)
	return nil
}

type syncCmd struct {
	Fixture      string `help:"Path to a fixture directory containing snapshot.json files."`
	Stdin        bool   `help:"Read a Snapshot JSON document from stdin and ingest it."`
	Entities     bool   `help:"Sync Linear reference data (teams, states, users, labels, projects). Requires LINEAR_API_KEY."`
	UpdatedSince string `help:"Live: sync issues updated at or after this RFC3339 timestamp or duration (e.g. 24h, 7d)." name:"updated-since"`
	Resume       bool   `help:"Live: resume issue sync from the stored high-water mark."`
	Issue        string `help:"Live: hydrate one issue by Linear UUID or TEAM-N identifier."`
	PageSize     int    `help:"Page size for paginated live queries." default:"100" name:"page-size"`
	MaxIssues    int    `help:"Stop after this many live issues (0 = unbounded)." default:"0" name:"max-issues"`
	DryRun       bool   `help:"Print what would be ingested without writing to the store." name:"dry-run"`
	NDJSON       bool   `help:"Live: stream each ingested issue as a JSON line instead of waiting for the final envelope."`
	JSON         bool   `help:"Emit JSON." default:"true" negatable:""`
}

func (c *syncCmd) Run(cc commandContext) error {
	modes := 0
	if c.Fixture != "" {
		modes++
	}
	if c.Stdin {
		modes++
	}
	if c.Entities {
		modes++
	}
	if c.UpdatedSince != "" {
		modes++
	}
	if c.Resume {
		modes++
	}
	if c.Issue != "" {
		modes++
	}
	if modes == 0 {
		return usageErr("sync: pick one of --fixture, --stdin, --entities, --updated-since, --resume, --issue")
	}
	if modes > 1 {
		return usageErr("sync: modes are mutually exclusive")
	}
	rt, err := config.LoadRuntime()
	if err != nil {
		return wrapErr(err, "config", ExitConfig)
	}
	if err := config.EnsureDirs(rt); err != nil {
		return wrapErr(err, "config", ExitConfig)
	}

	if c.Stdin && c.DryRun {
		return writeJSON(cc.stdout, map[string]any{
			"mode": "stdin",
			"note": "dry-run: would read Snapshot or NDJSON envelopes from stdin and ingest",
		})
	}

	lck, err := lock.Acquire(rt.DatabasePath)
	if err != nil {
		return wrapErr(err, "config", ExitConfig)
	}
	defer lck.Release()

	s, err := store.Open(rt.DatabasePath)
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	defer s.Close()

	if c.Stdin {
		count, err := s.IngestStream(os.Stdin, 200<<20)
		if err != nil {
			return validationErr(fmt.Sprintf("sync --stdin: %v", err))
		}
		counts, err := s.Counts()
		if err != nil {
			return wrapErr(err, "internal", ExitInternal)
		}
		return writeJSON(cc.stdout, struct {
			Source   string       `json:"source"`
			Ingested int          `json:"ingested"`
			Counts   store.Counts `json:"counts"`
		}{Source: "stdin", Ingested: count, Counts: counts})
	}

	if c.Fixture != "" {
		cwd, err := os.Getwd()
		if err != nil {
			return wrapErr(err, "internal", ExitInternal)
		}
		fixtureAbs, err := validateInputPath(c.Fixture, cwd)
		if err != nil {
			return err
		}
		if c.DryRun {
			snap, err := linear.LoadFixture(fixtureAbs)
			if err != nil {
				return wrapErr(err, "validation", ExitValidation)
			}
			plan := struct {
				Mode    string `json:"mode"`
				Fixture string `json:"fixture"`
				Would   struct {
					Teams    int `json:"teams"`
					States   int `json:"states"`
					Users    int `json:"users"`
					Labels   int `json:"labels"`
					Projects int `json:"projects"`
					Issues   int `json:"issues"`
				} `json:"would_ingest"`
			}{Mode: "fixture", Fixture: fixtureAbs}
			plan.Would.Teams = len(snap.Teams)
			plan.Would.States = len(snap.States)
			plan.Would.Users = len(snap.Users)
			plan.Would.Labels = len(snap.Labels)
			plan.Would.Projects = len(snap.Projects)
			plan.Would.Issues = len(snap.Issues)
			return writeJSON(cc.stdout, plan)
		}
		res, err := syncer.IngestFixture(s, fixtureAbs)
		if err != nil {
			return wrapErr(err, "validation", ExitValidation)
		}
		if c.JSON {
			return writeJSON(cc.stdout, res)
		}
		fmt.Fprintf(cc.stdout, "sync: fixture=%s teams=%d issues=%d comments=%d\n",
			res.Source, res.Counts.Teams, res.Counts.Issues, res.Counts.Comments)
		return nil
	}

	token := config.LinearAPIKey()
	if token == "" {
		return configErr("sync: LINEAR_API_KEY is unset; required for live sync")
	}
	client := linear.NewClient(rt.LinearAPIBase, token)

	if c.Entities {
		if c.DryRun {
			viewer, err := client.Viewer(cc.Context)
			if err != nil {
				return wrapErr(err, "internal", ExitInternal)
			}
			return writeJSON(cc.stdout, map[string]any{
				"mode":   "entities",
				"viewer": viewer,
				"note":   "dry-run: would paginate teams, states, users, labels, projects",
			})
		}
		res, err := syncer.IngestEntities(cc.Context, s, client)
		if err != nil {
			return wrapErr(err, "internal", ExitInternal)
		}
		if c.JSON {
			return writeJSON(cc.stdout, res)
		}
		fmt.Fprintf(cc.stdout, "sync: viewer=%s teams=%d states=%d users=%d labels=%d projects=%d\n",
			res.Viewer.Name, res.Counts.Teams, res.Counts.States, res.Counts.Users, res.Counts.Labels, res.Counts.Projects)
		return nil
	}

	if c.Issue != "" {
		validated, err := validateIssueRef(c.Issue)
		if err != nil {
			return err
		}
		if c.DryRun {
			return writeJSON(cc.stdout, map[string]any{
				"mode":  "issue",
				"issue": validated,
				"note":  "dry-run: would fetch one issue from Linear and upsert",
			})
		}
		res, err := syncer.IngestIssueByIdentifier(cc.Context, s, client, validated)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return wrapErr(err, "not_found", ExitNotFound)
			}
			return wrapErr(err, "internal", ExitInternal)
		}
		if c.JSON {
			return writeJSON(cc.stdout, res)
		}
		fmt.Fprintf(cc.stdout, "sync: issue=%s title=%q\n", res.Issue.Identifier, res.Issue.Title)
		return nil
	}

	var since time.Time
	if c.Resume {
		st, err := s.LoadCursor("issues.tail")
		if err != nil {
			return wrapErr(err, "internal", ExitInternal)
		}
		if st.HighWaterMark == "" {
			return usageErr("sync --resume: no stored high-water mark; run --updated-since first")
		}
		parsed, err := time.Parse(time.RFC3339, st.HighWaterMark)
		if err != nil {
			parsed, err = time.Parse("2006-01-02T15:04:05.999Z", st.HighWaterMark)
			if err != nil {
				return wrapErr(fmt.Errorf("parse high_water_mark %q: %w", st.HighWaterMark, err), "internal", ExitInternal)
			}
		}
		since = parsed
	} else {
		s2, err := parseSince(c.UpdatedSince)
		if err != nil {
			return err
		}
		since = s2
	}
	if c.DryRun {
		return writeJSON(cc.stdout, map[string]any{
			"mode":       "tail",
			"since":      since.UTC().Format(time.RFC3339),
			"page_size":  c.PageSize,
			"max_issues": c.MaxIssues,
			"note":       "dry-run: would call Linear issues(updatedAt >= since) with cursor pagination",
		})
	}
	if c.NDJSON {
		enc := json.NewEncoder(cc.stdout)
		total, err := syncer.StreamIssuesUpdatedSince(cc.Context, s, client, since, c.PageSize, c.MaxIssues, func(iss linear.Issue) error {
			return enc.Encode(iss)
		})
		if err != nil {
			return wrapErr(err, "internal", ExitInternal)
		}
		_ = total
		return nil
	}
	res, err := syncer.IngestIssuesUpdatedSince(cc.Context, s, client, since, c.PageSize, c.MaxIssues)
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	if c.JSON {
		return writeJSON(cc.stdout, res)
	}
	fmt.Fprintf(cc.stdout, "sync: since=%s pages=%d issues=%d comments=%d high_water=%s\n",
		res.Since, res.Pages, res.IssuesPulled, res.CommentsPulled, res.NewHighWater)
	return nil
}

func parseSince(in string) (time.Time, error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return time.Time{}, validationErr("sync: --updated-since is empty")
	}
	if t, err := time.Parse(time.RFC3339, in); err == nil {
		return t.UTC(), nil
	}
	expanded := in
	if strings.HasSuffix(expanded, "d") {
		n := strings.TrimSuffix(expanded, "d")
		hours, err := strconv.Atoi(n)
		if err != nil {
			return time.Time{}, validationErr(fmt.Sprintf("sync: invalid duration %q", in))
		}
		expanded = fmt.Sprintf("%dh", hours*24)
	}
	d, err := time.ParseDuration(expanded)
	if err != nil {
		return time.Time{}, validationErr(fmt.Sprintf("sync: invalid duration %q", in))
	}
	return time.Now().UTC().Add(-d), nil
}

type searchCmd struct {
	Query  string `arg:"" help:"Search input. Treated as a literal phrase unless --raw is set."`
	Limit  int    `help:"Max results." default:"50"`
	Raw    bool   `help:"Pass the query directly to SQLite FTS5 instead of quoting it as a phrase."`
	Fields string `help:"Comma-separated field whitelist applied to each result."`
	JSON   bool   `help:"Emit JSON." default:"true" negatable:""`
	NDJSON bool   `help:"Emit newline-delimited JSON instead of a JSON array."`
}

var searchResultFields = []string{
	"id",
	"identifier",
	"title",
	"team_key",
	"state_name",
	"state_type",
	"updated_at",
	"snippet",
	"score",
}

func (c *searchCmd) Run(cc commandContext) error {
	cleaned, err := validateQueryString(c.Query)
	if err != nil {
		return err
	}
	c.Query = cleaned
	rt, err := config.LoadRuntime()
	if err != nil {
		return wrapErr(err, "config", ExitConfig)
	}
	s, err := store.OpenReadOnly(rt.DatabasePath)
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	defer s.Close()
	query := c.Query
	if !c.Raw {
		query = store.PhraseQuery(c.Query)
	}
	results, err := s.Search(query, c.Limit)
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	if c.NDJSON {
		enc := json.NewEncoder(cc.stdout)
		for _, r := range results {
			raw, err := json.Marshal(r)
			if err != nil {
				return err
			}
			if c.Fields != "" {
				rows, err := projectListItemsWithKnown([]json.RawMessage{raw}, c.Fields, searchResultFields)
				if err != nil {
					return err
				}
				raw = rows[0]
			}
			if _, err := cc.stdout.Write(append(raw, '\n')); err != nil {
				return err
			}
			_ = enc
		}
		return nil
	}
	if c.JSON {
		if results == nil {
			results = []store.SearchResult{}
		}
		envelope := struct {
			Query   string               `json:"query"`
			Limit   int                  `json:"limit"`
			Results []store.SearchResult `json:"results"`
		}{Query: c.Query, Limit: c.Limit, Results: results}
		if c.Fields == "" {
			return writeJSON(cc.stdout, envelope)
		}
		rawItems := make([]json.RawMessage, 0, len(results))
		for _, r := range results {
			b, err := json.Marshal(r)
			if err != nil {
				return err
			}
			rawItems = append(rawItems, b)
		}
		projected, err := projectListItemsWithKnown(rawItems, c.Fields, searchResultFields)
		if err != nil {
			return err
		}
		out := struct {
			Query   string            `json:"query"`
			Limit   int               `json:"limit"`
			Results []json.RawMessage `json:"results"`
		}{Query: c.Query, Limit: c.Limit, Results: projected}
		return writeJSON(cc.stdout, out)
	}
	for _, r := range results {
		fmt.Fprintf(cc.stdout, "%s\t%s\t%s\n", r.Identifier, r.Title, r.Snippet)
	}
	return nil
}

type showCmd struct {
	ID     string `arg:"" help:"Linear UUID or identifier (e.g., LIN-12)."`
	Fields string `help:"Comma-separated field whitelist for the JSON output."`
	JSON   bool   `help:"Emit JSON." default:"true" negatable:""`
}

func (c *showCmd) Run(cc commandContext) error {
	validated, err := validateIssueRef(c.ID)
	if err != nil {
		return err
	}
	rt, err := config.LoadRuntime()
	if err != nil {
		return wrapErr(err, "config", ExitConfig)
	}
	s, err := store.OpenReadOnly(rt.DatabasePath)
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	defer s.Close()
	rec, err := s.Show(validated)
	if err != nil {
		if strings.Contains(err.Error(), "no such issue") {
			return wrapErr(err, "not_found", ExitNotFound)
		}
		return wrapErr(err, "internal", ExitInternal)
	}
	if c.JSON {
		return writeProjected(cc.stdout, rec, c.Fields)
	}
	fmt.Fprintf(cc.stdout, "%s\t%s\n%s\n", rec.Identifier, rec.Title, rec.Description)
	return nil
}

func resolveRecipient(flag string) (string, error) {
	if v := strings.TrimSpace(flag); v != "" {
		return v, nil
	}
	if v := strings.TrimSpace(os.Getenv(config.EnvAgeRecipient)); v != "" {
		return v, nil
	}
	return "", configErr("age recipient required: pass --recipient or set " + config.EnvAgeRecipient)
}

func resolveIdentity(flag string) (string, error) {
	if v := strings.TrimSpace(flag); v != "" {
		return v, nil
	}
	if v := strings.TrimSpace(os.Getenv(config.EnvAgeIdentity)); v != "" {
		return v, nil
	}
	if path := strings.TrimSpace(os.Getenv("LINCRAWL_AGE_IDENTITY_FILE")); path != "" {
		f, err := os.Open(path) // #nosec G304 G703 -- operator-supplied identity path outside any sandbox
		if err != nil {
			return "", wrapErr(err, "config", ExitConfig)
		}
		defer f.Close()
		// 64 KiB caps the read for any realistic age/ssh identity material;
		// a wrong env (e.g. /dev/urandom, /var/log/*) is rejected with a
		// typed error instead of OOM.
		const identityMaxBytes = 64 * 1024
		raw, err := io.ReadAll(io.LimitReader(f, identityMaxBytes+1))
		if err != nil {
			return "", wrapErr(err, "config", ExitConfig)
		}
		if int64(len(raw)) > identityMaxBytes {
			return "", configErr(fmt.Sprintf("LINCRAWL_AGE_IDENTITY_FILE %q exceeds %d bytes; not an age/ssh key", path, identityMaxBytes))
		}
		return string(raw), nil
	}
	return "", configErr("age identity required: pass --identity, set " + config.EnvAgeIdentity + ", or set LINCRAWL_AGE_IDENTITY_FILE")
}

func validatedOutPath(out, cwd string) (string, error) {
	if out == "" {
		return "", usageErr("--out is required")
	}
	if !strings.HasSuffix(out, ".jsonl.zst.age") {
		return "", validationErr("--out must end with .jsonl.zst.age")
	}
	abs, err := validateOutputPath(out, cwd)
	if err != nil {
		return "", err
	}
	return abs, nil
}

type archiveResult struct {
	Mode    string `json:"mode"`
	Fixture string `json:"fixture,omitempty"`
	Out     string `json:"out"`
	Records int    `json:"records"`
	Bytes   int64  `json:"bytes,omitempty"`
	DryRun  bool   `json:"dry_run,omitempty"`
}

func (c *archiveCmd) Run(cc commandContext) error {
	if c.Fixture == "" {
		return usageErr("archive: --fixture is required (use `publish` to archive the local store)")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	fixtureAbs, err := validateInputPath(c.Fixture, cwd)
	if err != nil {
		return err
	}
	// Resolve recipient + out path BEFORE dry-run short-circuit so the
	// plan reflects every preflight failure mode the real run would
	// hit.
	recipient, err := resolveRecipient(c.Recipient)
	if err != nil {
		return err
	}
	outAbs, err := validatedOutPath(c.Out, cwd)
	if err != nil {
		return err
	}
	snap, err := linear.LoadFixture(fixtureAbs)
	if err != nil {
		return wrapErr(err, "validation", ExitValidation)
	}
	records := archive.SnapshotRecords(snap)
	if c.DryRun {
		return writeJSON(cc.stdout, archiveResult{
			Mode: "archive-fixture", Fixture: fixtureAbs, Out: outAbs,
			Records: len(records), DryRun: true,
		})
	}
	if err := archive.WriteEncryptedJSONL(outAbs, recipient, records); err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	_ = recipient // keep the type for later; written via WriteEncryptedJSONL
	fi, _ := os.Stat(outAbs)
	return writeJSON(cc.stdout, archiveResult{
		Mode: "archive-fixture", Fixture: fixtureAbs, Out: outAbs,
		Records: len(records), Bytes: fi.Size(),
	})
}

type publishResult struct {
	Mode     string `json:"mode"`
	Database string `json:"database,omitempty"`
	Out      string `json:"out"`
	Records  int    `json:"records"`
	Bytes    int64  `json:"bytes,omitempty"`
	DryRun   bool   `json:"dry_run,omitempty"`
}

func (c *publishCmd) Run(cc commandContext) error {
	rt, err := config.LoadRuntime()
	if err != nil {
		return wrapErr(err, "config", ExitConfig)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	recipient, err := resolveRecipient(c.Recipient)
	if err != nil {
		return err
	}
	outAbs, err := validatedOutPath(c.Out, cwd)
	if err != nil {
		return err
	}
	s, err := store.OpenReadOnly(rt.DatabasePath)
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	defer s.Close()
	snap, err := s.Snapshot()
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	records := archive.SnapshotRecords(snap)
	if c.DryRun {
		return writeJSON(cc.stdout, publishResult{
			Mode: "publish", Database: rt.DatabasePath, Out: outAbs,
			Records: len(records), DryRun: true,
		})
	}
	if err := archive.WriteEncryptedJSONL(outAbs, recipient, records); err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	_ = recipient
	fi, _ := os.Stat(outAbs)
	return writeJSON(cc.stdout, publishResult{
		Mode: "publish", Database: rt.DatabasePath, Out: outAbs,
		Records: len(records), Bytes: fi.Size(),
	})
}

type importResult struct {
	Mode        string         `json:"mode"`
	In          string         `json:"in"`
	Records     int            `json:"records"`
	Counts      store.Counts   `json:"counts"`
	WouldIngest map[string]int `json:"would_ingest,omitempty"`
	DryRun      bool           `json:"dry_run,omitempty"`
}

func (c *importCmd) Run(cc commandContext) error {
	if c.In == "" {
		return usageErr("--in is required")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	// --in is intentionally NOT sandboxed under CWD — operators need to
	// import from a tenant store mount that lives anywhere on disk.
	// The sandbox guarantees (path traversal rejection, control chars)
	// in validateInputPath still apply.
	inAbs, err := validateInputPath(c.In, cwd)
	if err != nil {
		return err
	}
	identity, err := resolveIdentity(c.Identity)
	if err != nil {
		return err
	}
	records, err := archive.ReadEncryptedJSONL(inAbs, identity)
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	snap, err := archive.RecordsSnapshot(records)
	if err != nil {
		return wrapErr(err, "validation", ExitValidation)
	}
	if c.DryRun {
		return writeJSON(cc.stdout, importResult{
			Mode: "import", In: inAbs, Records: len(records),
			WouldIngest: snapshotCounts(snap), DryRun: true,
		})
	}
	rt, err := config.LoadRuntime()
	if err != nil {
		return wrapErr(err, "config", ExitConfig)
	}
	if err := config.EnsureDirs(rt); err != nil {
		return wrapErr(err, "config", ExitConfig)
	}
	lck, err := lock.Acquire(rt.DatabasePath)
	if err != nil {
		return wrapErr(err, "config", ExitConfig)
	}
	defer lck.Release()
	s, err := store.Open(rt.DatabasePath)
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	defer s.Close()
	if err := s.IngestSnapshot(snap); err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	counts, err := s.Counts()
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	return writeJSON(cc.stdout, importResult{
		Mode: "import", In: inAbs, Records: len(records), Counts: counts,
	})
}

func (c *storeVerifyCmd) Run(cc commandContext) error {
	cwd, err := os.Getwd()
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	rootAbs, err := validateInputPath(c.Path, cwd)
	if err != nil {
		return err
	}
	res, err := tenantstore.Verify(rootAbs)
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	if !res.OK {
		// Emit the full structured Result to stdout so agents keep every
		// finding + manifest detail, and return a typed error for the
		// classified envelope on stderr.
		if jerr := writeJSON(cc.stdout, res); jerr != nil {
			return wrapErr(jerr, "internal", ExitInternal)
		}
		msg := fmt.Sprintf("store verify: %d findings: %s", len(res.Findings), strings.Join(res.Findings, "; "))
		return &CLIError{Code: "validation", ExitVal: ExitValidation, Message: msg}
	}
	return writeJSON(cc.stdout, res)
}

func (c *subscribeCmd) Run(cc commandContext) error {
	cwd, err := os.Getwd()
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	rootAbs, err := validateInputPath(c.Path, cwd)
	if err != nil {
		return err
	}
	snaps, err := tenantstore.VerifiedSnapshots(rootAbs)
	if err != nil {
		return wrapErr(err, "validation", ExitValidation)
	}
	if c.DryRun {
		return writeJSON(cc.stdout, subscribeResult{
			Mode: "subscribe", Store: rootAbs,
			Snapshots: len(snaps), SnapshotPlan: snaps,
			DryRun: true,
		})
	}
	identity, err := resolveIdentity(c.Identity)
	if err != nil {
		return err
	}
	rt, err := config.LoadRuntime()
	if err != nil {
		return wrapErr(err, "config", ExitConfig)
	}
	if err := config.EnsureDirs(rt); err != nil {
		return wrapErr(err, "config", ExitConfig)
	}
	lck, err := lock.Acquire(rt.DatabasePath)
	if err != nil {
		return wrapErr(err, "config", ExitConfig)
	}
	defer lck.Release()
	s, err := store.Open(rt.DatabasePath)
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	defer s.Close()
	totalRecords := 0
	ingested := 0
	for i, snap := range snaps {
		records, err := archive.ReadEncryptedJSONL(snap.FullPath, identity)
		if err != nil {
			return wrapErr(fmt.Errorf("subscribe: snapshot %d/%d %s: decrypt (already-applied: %d): %w",
				i+1, len(snaps), snap.Path, ingested, err), "internal", ExitInternal)
		}
		fragment, err := archive.RecordsSnapshot(records)
		if err != nil {
			return wrapErr(fmt.Errorf("subscribe: snapshot %d/%d %s: parse (already-applied: %d): %w",
				i+1, len(snaps), snap.Path, ingested, err), "validation", ExitValidation)
		}
		if err := s.IngestSnapshot(fragment); err != nil {
			return wrapErr(fmt.Errorf("subscribe: snapshot %d/%d %s: ingest (already-applied: %d): %w",
				i+1, len(snaps), snap.Path, ingested, err), "internal", ExitInternal)
		}
		totalRecords += len(records)
		ingested++
	}
	counts, err := s.Counts()
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	return writeJSON(cc.stdout, subscribeResult{
		Mode: "subscribe", Store: rootAbs,
		Snapshots: len(snaps), Ingested: ingested,
		Records: totalRecords, Counts: &counts,
	})
}

type subscribeResult struct {
	Mode         string                         `json:"mode"`
	Store        string                         `json:"store"`
	Snapshots    int                            `json:"snapshots"`
	Ingested     int                            `json:"ingested,omitempty"`
	Records      int                            `json:"records,omitempty"`
	Counts       *store.Counts                  `json:"counts,omitempty"`
	SnapshotPlan []tenantstore.VerifiedSnapshot `json:"snapshot_plan,omitempty"`
	DryRun       bool                           `json:"dry_run,omitempty"`
}

func snapshotCounts(snap linear.Snapshot) map[string]int {
	return map[string]int{
		"teams":    len(snap.Teams),
		"states":   len(snap.States),
		"users":    len(snap.Users),
		"labels":   len(snap.Labels),
		"projects": len(snap.Projects),
		"issues":   len(snap.Issues),
	}
}

type queryCmd struct {
	Graphql     string `help:"Inline GraphQL query/mutation text." name:"graphql"`
	GraphqlFile string `help:"Path to a file containing the GraphQL query text." name:"graphql-file"`
	Vars        string `help:"JSON object of variables to bind." name:"vars"`
	JSON        bool   `help:"Emit JSON." default:"true" negatable:""`
}

func (c *queryCmd) Run(cc commandContext) error {
	if (c.Graphql == "") == (c.GraphqlFile == "") {
		return usageErr("query: provide exactly one of --graphql or --graphql-file")
	}
	rt, err := config.LoadRuntime()
	if err != nil {
		return wrapErr(err, "config", ExitConfig)
	}
	token := config.LinearAPIKey()
	if token == "" {
		return configErr("query: LINEAR_API_KEY is unset")
	}
	queryText := c.Graphql
	if c.GraphqlFile != "" {
		cwd, err := os.Getwd()
		if err != nil {
			return wrapErr(err, "internal", ExitInternal)
		}
		abs, err := validateSandboxedInputPath("--graphql-file", c.GraphqlFile, cwd)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(abs)
		if err != nil {
			return wrapErr(err, "validation", ExitValidation)
		}
		queryText = string(raw)
	}
	if cleaned, err := validateQueryString(queryText); err != nil {
		return err
	} else {
		queryText = cleaned
	}
	vars := map[string]any{}
	if c.Vars != "" {
		if err := json.Unmarshal([]byte(c.Vars), &vars); err != nil {
			return validationErr(fmt.Sprintf("query: --vars must be a JSON object: %v", err))
		}
	}
	client := linear.NewClient(rt.LinearAPIBase, token)
	data, err := client.Query(cc.Context, queryText, vars)
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	var pretty any
	if err := json.Unmarshal(data, &pretty); err != nil {
		return wrapErr(fmt.Errorf("decode response: %w", err), "internal", ExitInternal)
	}
	return writeJSON(cc.stdout, pretty)
}

type exportCmd struct {
	Out  string `help:"Output path for NDJSON (sandboxed under CWD). Use - for stdout." default:"-"`
	JSON bool   `help:"Emit a JSON status envelope after writing." default:"true" negatable:""`
}

func (c *exportCmd) Run(cc commandContext) error {
	rt, err := config.LoadRuntime()
	if err != nil {
		return wrapErr(err, "config", ExitConfig)
	}
	s, err := store.OpenReadOnly(rt.DatabasePath)
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	defer s.Close()
	target := c.Out
	if target == "-" || target == "" {
		count, err := s.ExportNDJSON(cc.stdout)
		if err != nil {
			return wrapErr(err, "internal", ExitInternal)
		}
		_ = count
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	abs, err := validateOutputPath(target, cwd)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	count, writeErr := s.ExportNDJSON(f)
	closeErr := f.Close()
	if writeErr != nil {
		return wrapErr(writeErr, "internal", ExitInternal)
	}
	if closeErr != nil {
		return wrapErr(closeErr, "internal", ExitInternal)
	}
	if c.JSON {
		return writeJSON(cc.stdout, map[string]any{"out": abs, "records": count})
	}
	fmt.Fprintf(cc.stdout, "export: %d records → %s\n", count, abs)
	return nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeProjected(w io.Writer, v any, fields string) error {
	b, err := projectFields(v, fields)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}
