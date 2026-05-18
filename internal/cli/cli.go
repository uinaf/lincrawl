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

	"github.com/uinaf/lincrawl/internal/buildinfo"
	"github.com/uinaf/lincrawl/internal/config"
	"github.com/uinaf/lincrawl/internal/linear"
	"github.com/uinaf/lincrawl/internal/store"
	"github.com/uinaf/lincrawl/internal/syncer"
)

type rootCmd struct {
	Doctor   doctorCmd   `cmd:"" help:"Check local configuration."`
	Describe describeCmd `cmd:"" help:"Print machine-readable command schemas."`
	Status   statusCmd   `cmd:"" help:"Print local archive status."`
	Sync     syncCmd     `cmd:"" help:"Sync issues from fixtures or Linear."`
	Search   searchCmd   `cmd:"" help:"Search the local archive."`
	Show     showCmd     `cmd:"" help:"Show one local issue by id or identifier."`
	Query    queryCmd    `cmd:"" help:"Run a raw Linear GraphQL query."`
	Export   exportCmd   `cmd:"" help:"Export the local archive as canonical JSONL."`
	Version  versionCmd  `cmd:"" help:"Print version information."`
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

type kongExit int
type errParserExit struct{ code int }

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
	JSON bool `help:"Emit JSON." default:"true" negatable:""`
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
	Name             string       `json:"name"`
	Help             string       `json:"help"`
	Args             []argSpec    `json:"args"`
	Flags            []flagSpec   `json:"flags"`
	MutuallyExclusive [][]string  `json:"mutually_exclusive,omitempty"`
	Example          string       `json:"example,omitempty"`
}

var mutuallyExclusiveByCommand = map[string][][]string{
	"sync":  {{"--fixture", "--stdin", "--entities", "--updated-since", "--resume", "--issue"}},
	"query": {{"--graphql", "--graphql-file"}},
}

type describeResult struct {
	Tool      string                       `json:"tool"`
	Version   string                       `json:"version"`
	ExitCodes map[string]int               `json:"exit_codes"`
	FieldMasks map[string][]string         `json:"field_masks"`
	Commands  []commandDescription         `json:"commands"`
}

var commandExamples = map[string]string{
	"doctor":   "lincrawl doctor --offline --json",
	"describe": "lincrawl describe --json",
	"status":   "lincrawl status --json --fields counts",
	"sync":     "lincrawl sync --updated-since 24h --max-issues 200 --json",
	"search":   `lincrawl search "ingest" --fields identifier,title --json`,
	"show":     "lincrawl show LIN-1 --fields identifier,title,labels --json",
	"query":    `lincrawl query --graphql 'query { viewer { id name } }' --json`,
	"export":   "lincrawl export --out ./snapshots/lincrawl.jsonl --json",
	"version":  "lincrawl version --json",
}

func (c *describeCmd) Run(cc commandContext) error {
	var root rootCmd
	parser, err := kong.New(&root, kong.Name("lincrawl"), kong.Exit(func(int) {}))
	if err != nil {
		return wrapErr(err, "internal", ExitInternal)
	}
	model := parser.Model
	result := describeResult{
		Tool:    "lincrawl",
		Version: buildinfo.Current().Version,
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
	for _, node := range model.Node.Children {
		if node.Type != kong.CommandNode {
			continue
		}
		cmd := commandDescription{
			Name:    node.Name,
			Help:    node.Help,
			Args:    []argSpec{},
			Flags:   []flagSpec{},
			Example: commandExamples[node.Name],
		}
		for _, pos := range node.Positional {
			cmd.Args = append(cmd.Args, argSpec{
				Name:     pos.Name,
				Type:     pos.Target.Type().String(),
				Required: pos.Required,
				Help:     pos.Help,
			})
		}
		for _, f := range node.Flags {
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
		if groups, ok := mutuallyExclusiveByCommand[node.Name]; ok {
			cmd.MutuallyExclusive = groups
		}
		result.Commands = append(result.Commands, cmd)
	}
	if c.JSON {
		return writeJSON(cc.stdout, result)
	}
	for _, cmd := range result.Commands {
		fmt.Fprintf(cc.stdout, "%-10s %s\n", cmd.Name, cmd.Help)
	}
	return nil
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
	OK             bool   `json:"ok"`
	Offline        bool   `json:"offline"`
	Home           string `json:"home"`
	DatabasePath   string `json:"database_path"`
	ConfigDir      string `json:"config_dir"`
	DotEnvPath     string `json:"dotenv_path"`
	DotEnvLoaded   bool   `json:"dotenv_loaded"`
	LinearAPIBase  string `json:"linear_api_base"`
	LinearAPIToken string `json:"linear_api_token"`
	AgeRecipient   string `json:"age_recipient"`
	AgeIdentity    string `json:"age_identity"`
	Notes          []string `json:"notes,omitempty"`
}

func (c *doctorCmd) Run(cc commandContext) error {
	rt, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	if err := config.EnsureDirs(rt); err != nil {
		return err
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
	Home         string       `json:"home"`
	DatabasePath string       `json:"database_path"`
	Exists       bool         `json:"exists"`
	Counts       store.Counts `json:"counts"`
}

func (c *statusCmd) Run(cc commandContext) error {
	rt, err := config.LoadRuntime()
	if err != nil {
		return err
	}
	res := statusResult{Home: rt.Home, DatabasePath: rt.DatabasePath}
	if _, err := os.Stat(rt.DatabasePath); err == nil {
		res.Exists = true
		s, err := store.Open(rt.DatabasePath)
		if err != nil {
			return err
		}
		defer s.Close()
		counts, err := s.Counts()
		if err != nil {
			return err
		}
		res.Counts = counts
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
	s, err := store.Open(rt.DatabasePath)
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
				rows, err := projectListItems([]json.RawMessage{raw}, c.Fields)
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
		projected, err := projectListItems(rawItems, c.Fields)
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
	s, err := store.Open(rt.DatabasePath)
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
		abs, err := validateOutputPath(c.GraphqlFile, cwd)
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
	s, err := store.Open(rt.DatabasePath)
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
