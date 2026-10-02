package crmcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	catalog "github.com/qianlan33333-png/AI-CRM-v3/internal/clicatalog/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type Options struct {
	Profile, Format string
	Timeout         time.Duration
	In              io.Reader
	Out, Err        io.Writer
}

func (o *Options) client() (*Client, error) {
	cfg, e := LoadConfig()
	if e != nil {
		return nil, e
	}
	name := o.Profile
	if name == "" {
		name = cfg.Current
	}
	p, ok := cfg.Profiles[name]
	if !ok {
		return nil, fail("configuration", "select an existing profile", 2)
	}
	credential, e := LoadCredential(p.Origin)
	if e != nil {
		return nil, e
	}
	return NewClient(p.Origin, credential, o.Timeout)
}
func (o *Options) print(value any) error {
	if o.Format != "json" && o.Format != "table" && o.Format != "ndjson" {
		return fail("validation", "format must be json, table or ndjson", 2)
	}
	if o.Format == "table" {
		b, err := json.Marshal(value)
		if err != nil {
			return err
		}
		var fields map[string]any
		if err = json.Unmarshal(b, &fields); err != nil {
			return err
		}
		keys := make([]string, 0, len(fields))
		for key := range fields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		w := tabwriter.NewWriter(o.Out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "FIELD\tVALUE")
		for _, key := range keys {
			cell, err := json.Marshal(fields[key])
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "%s\t%s\n", key, cell)
		}
		return w.Flush()
	}
	return json.NewEncoder(o.Out).Encode(value)
}
func Run(args []string, in io.Reader, out, stderr io.Writer) int {
	o := &Options{In: in, Out: out, Err: stderr}
	root := NewCommand(o)
	root.SetArgs(args)
	root.SetIn(in)
	root.SetOut(out)
	root.SetErr(stderr)
	if e := root.Execute(); e != nil {
		var f *Failure
		if !errors.As(e, &f) {
			f = &Failure{Code: "runtime", Message: "command failed; inspect configuration or authorized server audit", Exit: 1}
		}
		value := map[string]any{"error": f}
		if f.Result != nil {
			value["result"] = f.Result
		}
		_ = json.NewEncoder(out).Encode(value)
		return f.Exit
	}
	return 0
}
func NewCommand(o *Options) *cobra.Command {
	root := &cobra.Command{Use: "aicrm-cli", Short: "CRM business operations; human and machine authorization remain independent", SilenceUsage: true, SilenceErrors: true}
	root.PersistentPreRunE = func(*cobra.Command, []string) error {
		if o.Format != "json" && o.Format != "table" && o.Format != "ndjson" {
			return fail("validation", "format must be json, table or ndjson", 2)
		}
		if o.Timeout <= 0 {
			return fail("validation", "request timeout must be positive", 2)
		}
		return nil
	}
	root.PersistentFlags().StringVar(&o.Profile, "profile", "", "configured server profile")
	root.PersistentFlags().StringVar(&o.Format, "format", "json", "json, ndjson or table")
	root.PersistentFlags().DurationVar(&o.Timeout, "timeout", 30*time.Second, "client request timeout")
	root.AddCommand(profileCommands(o), authCommands(o))
	caps := &cobra.Command{Use: "capabilities", Short: "compiled inventory and source evidence"}
	caps.AddCommand(&cobra.Command{Use: "list", RunE: func(*cobra.Command, []string) error { return o.print(catalog.Load()) }})
	caps.AddCommand(&cobra.Command{Use: "live", Short: "current server grants; resource conditions still checked at execution", RunE: func(cmd *cobra.Command, _ []string) error {
		c, e := o.client()
		if e != nil {
			return e
		}
		path := "/open/v2/cli/context"
		if c.Credential.Kind == "human" {
			path = "/api/admin/cli/capabilities"
		}
		return o.read(ctx(cmd), c, path)
	}})
	root.AddCommand(caps)
	schema := &cobra.Command{Use: "schema"}
	schema.AddCommand(&cobra.Command{Use: "cli", Args: cobra.NoArgs, Short: "additive V2 authentication and discovery contracts", RunE: func(*cobra.Command, []string) error { return o.print(catalog.Load().CLIContract) }})
	schema.AddCommand(&cobra.Command{Use: "get OPERATION_ID", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		op, ok := catalog.Find(args[0])
		if !ok {
			return fail("not_found", "unknown operation ID", 2)
		}
		return o.print(map[string]any{"operation": op, "definitions": catalog.Load().Schemas})
	}})
	root.AddCommand(schema)
	invoke := &cobra.Command{Use: "invoke OPERATION_ID", Args: cobra.ExactArgs(1)}
	addCallFlags(invoke)
	invoke.RunE = func(cmd *cobra.Command, args []string) error {
		op, ok := catalog.Find(args[0])
		if !ok {
			return fail("not_found", "unregistered operation ID", 2)
		}
		return o.invoke(cmd, op)
	}
	root.AddCommand(invoke)
	groups := map[string]*cobra.Command{"capabilities": caps}
	for _, op := range catalog.Load().Operations {
		op := op
		g := groups[op.Group]
		if g == nil {
			g = &cobra.Command{Use: op.Group, Short: "registered " + op.Group + " business operations"}
			groups[op.Group] = g
			root.AddCommand(g)
		}
		parts := strings.Fields(op.Command)
		if len(parts) == 0 {
			parts = []string{op.ID}
		}
		parent := g
		for _, segment := range parts[:len(parts)-1] {
			var resource *cobra.Command
			for _, existing := range parent.Commands() {
				if existing.Name() == segment {
					resource = existing
					break
				}
			}
			if resource == nil {
				resource = &cobra.Command{Use: segment}
				parent.AddCommand(resource)
			}
			parent = resource
		}
		use := parts[len(parts)-1]
		cmd := &cobra.Command{Use: use, Short: op.Summary, Long: op.Summary + "\nOperation ID: " + op.ID + "\nEvidence: " + op.Evidence + "\nAuthorization is checked by the owning server route.", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return o.invoke(cmd, op) }}
		addCallFlags(cmd)
		parent.AddCommand(cmd)
	}
	root.AddCommand(&cobra.Command{Use: "doctor", RunE: func(cmd *cobra.Command, _ []string) error {
		c, e := o.client()
		if e != nil {
			return e
		}
		path := "/open/v2/cli/context"
		if c.Credential.Kind == "human" {
			path = "/api/admin/access/cli/session"
		}
		resp, e := c.request(ctx(cmd), "GET", path, "", nil, "")
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		var self map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&self); err != nil || self["kind"] != c.Credential.Kind {
			return fail("protocol", "server identity response does not match the authenticated identity kind", 1)
		}
		serverOrigin, _ := self["server_origin"].(string)
		binding := "server_origin_not_reported"
		if serverOrigin != "" {
			reported, err := Origin(serverOrigin)
			if err != nil {
				return fail("protocol", "server reported an invalid origin", 1)
			}
			if reported == c.Origin {
				binding = "matches_transport_origin"
			} else {
				return &Failure{Code: "environment_mismatch", Message: "server configured origin differs from profile; inspect environment before business writes", Exit: 4, Result: map[string]any{"origin": c.Origin, "server_origin": reported}}
			}
		}
		return o.print(map[string]any{"origin": c.Origin, "server_origin": serverOrigin, "environment_binding": binding, "identity_kind": c.Credential.Kind, "authentication": "verified", "provider_readiness": "not_proven_by_authentication"})
	}})
	return root
}
func ctx(cmd *cobra.Command) context.Context { return cmd.Context() }
func addCallFlags(cmd *cobra.Command) {
	cmd.Flags().StringArray("param", nil, "registered path parameter name=value")
	cmd.Flags().StringArray("query", nil, "query parameter name=value; server validates domain fields")
	cmd.Flags().String("input", "", "JSON file, binary file or - for stdin")
	cmd.Flags().StringArray("upload", nil, "multipart field=local-path")
	cmd.Flags().String("idempotency-key", "", "original logical operation key")
	cmd.Flags().String("output", "", "protected download file; refuses overwrite")
	cmd.Flags().String("secret-output", "", "protected file for one-time issued secrets")
	cmd.Flags().Bool("unrestricted-owner-scope", false, "explicitly permit an unrestricted machine client owner scope")
	cmd.Flags().Bool("dry-run", false, "local validation only; no server request")
	cmd.Flags().Bool("all", false, "follow explicit next_cursor; refuse unsupported pagination")
	cmd.Flags().String("cursor-field", "next_cursor", "response field for pagination")
	cmd.Flags().String("cursor-query", "cursor", "request cursor parameter")
	cmd.Flags().Bool("wait", false, "poll this registered GET status operation until terminal")
	cmd.Flags().Duration("wait-timeout", 5*time.Minute, "maximum status wait")
	cmd.Flags().String("status-field", "status", "status field for wait")
}
func pairs(values []string) (map[string]string, error) {
	m := map[string]string{}
	for _, v := range values {
		p := strings.SplitN(v, "=", 2)
		if len(p) != 2 || p[0] == "" {
			return nil, fail("validation", "expected name=value", 2)
		}
		if _, ok := m[p[0]]; ok {
			return nil, fail("validation", "duplicate parameter", 2)
		}
		m[p[0]] = p[1]
	}
	return m, nil
}
func readInput(o *Options, path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	if path == "-" {
		return io.ReadAll(o.In)
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, fail("io", "cannot read input file", 1)
	}
	return b, nil
}
func secretOperation(op catalog.Operation) bool {
	if op.Binding == nil {
		return false
	}
	p := op.Binding.Path
	return strings.Contains(p, "/open-platform/clients") && (op.Binding.Method == "POST") && (strings.HasSuffix(p, "/rotate") || strings.HasSuffix(p, "/clients"))
}
func (o *Options) invoke(cmd *cobra.Command, op catalog.Operation) error {
	parameters, _ := cmd.Flags().GetStringArray("param")
	query, _ := cmd.Flags().GetStringArray("query")
	uploads, _ := cmd.Flags().GetStringArray("upload")
	file, _ := cmd.Flags().GetString("input")
	key, _ := cmd.Flags().GetString("idempotency-key")
	input := CallInput{IdempotencyKey: key}
	var e error
	if input.Parameters, e = pairs(parameters); e != nil {
		return e
	}
	if input.Query, e = pairs(query); e != nil {
		return e
	}
	if input.Uploads, e = pairs(uploads); e != nil {
		return e
	}
	if input.Body, e = readInput(o, file); e != nil {
		return e
	}
	unrestricted, _ := cmd.Flags().GetBool("unrestricted-owner-scope")
	if e = validateScopeIntent(op, input.Body, unrestricted); e != nil {
		return e
	}
	_, _, validatedBody, e := BuildRequest(op, input)
	if e != nil {
		return e
	}
	if closer, ok := validatedBody.(io.Closer); ok {
		closer.Close()
	}
	dry, _ := cmd.Flags().GetBool("dry-run")
	if dry {
		return o.print(map[string]any{"operation_id": op.ID, "method": op.Binding.Method, "path_template": op.Binding.Path, "validation": "local_structural_only", "server_authorization_checked": false, "executed": false})
	}
	secret, _ := cmd.Flags().GetString("secret-output")
	if secretOperation(op) && secret == "" {
		return fail("validation", "this operation issues a secret; provide --secret-output", 2)
	}
	output, _ := cmd.Flags().GetString("output")
	if output != "" && secret != "" {
		return fail("validation", "choose output or secret-output", 2)
	}
	all, _ := cmd.Flags().GetBool("all")
	wait, _ := cmd.Flags().GetBool("wait")
	if (all || wait) && op.Binding.Method != "GET" {
		return fail("validation", "pagination and waiting only apply to registered GET operations", 2)
	}
	if all && wait {
		return fail("validation", "choose pagination or waiting", 2)
	}
	if (all || wait) && (output != "" || secret != "") {
		return fail("validation", "file output cannot be combined with pagination or waiting", 2)
	}
	if all || wait {
		if o.Format == "table" {
			return fail("validation", "pagination requires ndjson output", 2)
		}
		o.Format = "ndjson"
	}
	cursorField, _ := cmd.Flags().GetString("cursor-field")
	cursorQuery, _ := cmd.Flags().GetString("cursor-query")
	statusField, _ := cmd.Flags().GetString("status-field")
	duration, _ := cmd.Flags().GetDuration("wait-timeout")
	if wait && duration <= 0 {
		return fail("validation", "wait timeout must be positive", 2)
	}
	c, e := o.client()
	if e != nil {
		return e
	}
	var destination *os.File
	if target := output; target != "" || secret != "" {
		if all || wait {
			return fail("validation", "file output cannot be combined with pagination or waiting", 2)
		}
		if secret != "" {
			target = secret
		}
		destination, e = reserveOutput(target)
		if e != nil {
			return e
		}
		defer destination.Close()
	}
	deadline := time.Now().Add(duration)
	seen := map[string]bool{}
	for {
		resp, e := c.Call(ctx(cmd), op, input)
		if e != nil {
			return e
		}
		if output != "" || secret != "" {
			target := output
			if secret != "" {
				target = secret
			}
			_, e := io.Copy(destination, resp.Body)
			resp.Body.Close()
			if e != nil {
				if op.Binding.Method != "GET" {
					return fail("outcome_unknown", "write response download incomplete; query the original operation before retrying", 7)
				}
				return fail("io", "response download incomplete", 1)
			}
			if e = destination.Sync(); e != nil {
				return fail("io", "output could not be durably saved; do not repeat the business operation", 1)
			}
			return o.print(map[string]any{"operation_id": op.ID, "file": target, "http_status": resp.StatusCode, "business_success": "consult_result"})
		}
		if resp.StatusCode == http.StatusNoContent {
			resp.Body.Close()
			if all || wait {
				return fail("protocol", "no-content response cannot establish pagination or terminal business status", 1)
			}
			return o.print(map[string]any{"operation_id": op.ID, "origin": c.Origin, "http_status": resp.StatusCode, "business_success": "consult_owner_readback"})
		}
		typ := resp.Header.Get("Content-Type")
		body, e := io.ReadAll(resp.Body)
		resp.Body.Close()
		if e != nil {
			if op.Binding.Method != "GET" {
				return fail("outcome_unknown", "write response incomplete; query the original operation before retrying", 7)
			}
			return fail("io", "response incomplete; query original operation", 1)
		}
		if !strings.Contains(typ, "json") {
			if op.Binding.Method != "GET" {
				return fail("outcome_unknown", "write returned an unexpected response type; inspect the original operation without resubmitting", 7)
			}
			return fail("output_required", "non-JSON response requires --output; request is not retried", 2)
		}
		var result any
		if e = StrictJSON(body, &result); e != nil {
			if op.Binding.Method != "GET" {
				return fail("outcome_unknown", "write response is invalid; query the original operation before retrying", 7)
			}
			return fail("protocol", "server response is not valid JSON", 1)
		}
		if secretOperation(op) {
			return fail("secret_output_required", "one-time secret output is required", 2)
		}
		envelope := map[string]any{"operation_id": op.ID, "origin": c.Origin, "http_status": resp.StatusCode, "data": result, "business_success": "consult_result"}
		if all || wait {
			if e = o.print(envelope); e != nil {
				return e
			}
		}
		resultFailure := func(code, message string, exit int) error {
			f := &Failure{Code: code, Message: message, Exit: exit}
			if !all && !wait {
				f.Result = envelope
			}
			return f
		}
		m, _ := result.(map[string]any)
		if m["ok"] == false {
			return resultFailure("business_rejected", "server returned an unsuccessful business result", 1)
		}
		if data, ok := m["data"].(map[string]any); ok {
			m = data
		}
		if m["status"] == "outcome_unknown" {
			return resultFailure("outcome_unknown", "query and reconcile the original operation", 7)
		}
		if m["status"] == "partial" || m["status"] == "partially_completed" {
			return resultFailure("partial", "inspect per-item results; do not retry unknown items", 8)
		}
		if m["status"] == "failed" || m["status"] == "cancelled" || m["status"] == "canceled" {
			return resultFailure("operation_failed", "operation reached an unsuccessful terminal state", 1)
		}
		if all {
			next, present := m[cursorField]
			if !present {
				return fail("pagination_unavailable", "response has no contracted cursor field; no further request made", 2)
			}
			token, ok := next.(string)
			if next == nil {
				token = ""
				ok = true
			}
			if !ok {
				return fail("protocol", "cursor must be a string", 1)
			}
			if token == "" {
				return nil
			}
			if seen[token] {
				return fail("protocol", "server repeated cursor; stopped pagination", 1)
			}
			seen[token] = true
			if op.Binding.CursorOnly {
				input.Query = map[string]string{}
			}
			input.Query[cursorQuery] = token
			continue
		}
		if wait {
			status, _ := m[statusField].(string)
			switch status {
			case "completed", "succeeded", "reconciled":
				return nil
			case "executed":
				return fail("delivery_unverified", "execution is recorded; delivery or business outcome remains unverified", 6)
			case "outcome_unknown":
				return fail("outcome_unknown", "reconcile original operation", 7)
			case "failed", "cancelled", "canceled":
				return fail("operation_failed", "operation reached an unsuccessful terminal state", 1)
			case "partial", "partially_completed":
				return fail("partial", "batch completed only partially", 8)
			case "accepted", "queued", "pending", "running", "attempted", "awaiting_employee":
			default:
				return fail("unsupported_status", "unknown status; inspect result without retrying writes", 1)
			}
			if time.Now().After(deadline) {
				return fail("pending", "wait timed out; original operation remains queryable", 6)
			}
			select {
			case <-ctx(cmd).Done():
				return fail("pending", "wait cancelled", 6)
			case <-time.After(time.Second):
			}
			continue
		}
		return o.print(envelope)
	}
}

// Empty OwnerScope already means unrestricted in the server contract. Do not
// turn an omitted creation field or an accidental clearing into a broad grant.
// Patches without owner_scope retain the original value, as the Owner specifies.
func validateScopeIntent(op catalog.Operation, body []byte, unrestricted bool) error {
	if op.ID != "admin.createOpenPlatformClient" && op.ID != "admin.patchOpenPlatformClient" {
		return nil
	}
	var value map[string]any
	if e := StrictJSON(body, &value); e != nil {
		return e
	}
	scope, present := value["owner_scope"]
	if !present && op.ID == "admin.patchOpenPlatformClient" {
		return nil
	}
	if !present {
		return fail("validation", "client creation requires an explicit owner_scope field", 2)
	}
	if scope == nil && op.ID == "admin.createOpenPlatformClient" {
		return fail("validation", "creation owner_scope must be an object", 2)
	}
	if scope == nil {
		if !unrestricted {
			return fail("validation", "clearing owner_scope requires --unrestricted-owner-scope", 2)
		}
		return nil
	}
	m, ok := scope.(map[string]any)
	if !ok {
		return fail("validation", "owner_scope must be an object", 2)
	}
	if len(m) == 0 && !unrestricted {
		return fail("validation", "empty owner_scope is unrestricted; declare --unrestricted-owner-scope", 2)
	}
	for _, v := range m {
		if a, ok := v.([]any); ok && len(a) == 0 {
			return fail("validation", "owner_scope values cannot be empty arrays", 2)
		}
	}
	return nil
}
func (o *Options) read(ctx context.Context, c *Client, path string) error {
	resp, e := c.request(ctx, "GET", path, "", nil, "")
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	var value any
	if e = json.NewDecoder(resp.Body).Decode(&value); e != nil {
		return fail("protocol", "invalid response", 1)
	}
	return o.print(value)
}
func profileCommands(o *Options) *cobra.Command {
	root := &cobra.Command{Use: "profile"}
	root.AddCommand(&cobra.Command{Use: "list", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		c, e := LoadConfig()
		if e != nil {
			return e
		}
		return o.print(c)
	}})
	root.AddCommand(&cobra.Command{Use: "add NAME HTTPS_ORIGIN", Args: cobra.ExactArgs(2), RunE: func(_ *cobra.Command, args []string) error {
		origin, e := Origin(args[1])
		if e != nil {
			return e
		}
		c, e := LoadConfig()
		if e != nil {
			return e
		}
		if _, exists := c.Profiles[args[0]]; exists {
			return fail("conflict", "profile already exists; use a new name", 5)
		}
		c.Profiles[args[0]] = Profile{Origin: origin}
		if c.Current == "" {
			c.Current = args[0]
		}
		if e = SaveConfig(c); e != nil {
			return e
		}
		return o.print(map[string]any{"profile": args[0], "origin": origin})
	}})
	root.AddCommand(&cobra.Command{Use: "use NAME", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		c, e := LoadConfig()
		if e != nil {
			return e
		}
		if _, ok := c.Profiles[args[0]]; !ok {
			return fail("configuration", "unknown profile", 2)
		}
		c.Current = args[0]
		return SaveConfig(c)
	}})
	return root
}
func secretInput(o *Options, stdin bool, env string) (string, error) {
	environment := platformconfig.LoadCLIEnvironment()
	v := environment.Password
	if env == "AICRM_CLI_CLIENT_SECRET" {
		v = environment.ClientSecret
	}
	if v != "" && !stdin {
		return v, nil
	}
	if stdin {
		b, e := io.ReadAll(o.In)
		return strings.TrimRight(string(b), "\r\n"), e
	}
	f, ok := o.In.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return "", fail("authentication", "secret requires hidden terminal input, stdin or protected environment", 3)
	}
	fmt.Fprint(o.Err, "Password/secret: ")
	b, e := term.ReadPassword(int(f.Fd()))
	fmt.Fprintln(o.Err)
	return string(b), e
}
func authCommands(o *Options) *cobra.Command {
	root := &cobra.Command{Use: "auth"}
	var username, clientID, scope string
	var stdin bool
	login := &cobra.Command{Use: "login", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if username != "" && clientID != "" {
			return fail("validation", "select either a human username or a machine client ID", 2)
		}
		cfg, e := LoadConfig()
		if e != nil {
			return e
		}
		name := o.Profile
		if name == "" {
			name = cfg.Current
		}
		p, ok := cfg.Profiles[name]
		if !ok {
			return fail("configuration", "select a profile", 2)
		}
		c, e := NewClient(p.Origin, Credential{}, o.Timeout)
		if e != nil {
			return e
		}
		jar, _ := cookiejar.New(nil)
		c.HTTP.Jar = jar
		credential := Credential{Origin: c.Origin}
		var resp *http.Response
		if clientID != "" {
			secret, e := secretInput(o, stdin, "AICRM_CLI_CLIENT_SECRET")
			if e != nil {
				return e
			}
			form := url.Values{"grant_type": {"client_credentials"}, "client_id": {clientID}, "client_secret": {secret}, "audience": {"external_integration"}, "scope": {scope}}
			resp, e = c.request(ctx(cmd), "POST", "/oauth/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), "")
			if e != nil {
				return e
			}
			defer resp.Body.Close()
			var v struct {
				Token string `json:"access_token"`
			}
			if e = json.NewDecoder(resp.Body).Decode(&v); e != nil || v.Token == "" {
				return fail("protocol", "token response unavailable", 1)
			}
			credential.Kind = "machine"
			credential.Token = v.Token
		} else {
			if username == "" {
				return fail("validation", "human login requires --username", 2)
			}
			resp, e = c.request(ctx(cmd), "GET", "/api/admin/access/cli/login-challenge", "", nil, "")
			if e != nil {
				return e
			}
			var v struct {
				Token string `json:"login_csrf_token"`
			}
			e = json.NewDecoder(resp.Body).Decode(&v)
			resp.Body.Close()
			if e != nil || v.Token == "" {
				return fail("protocol", "login challenge unavailable", 1)
			}
			password, e := secretInput(o, stdin, "AICRM_CLI_PASSWORD")
			if e != nil {
				return e
			}
			b, _ := json.Marshal(map[string]string{"username": username, "password": password, "login_csrf_token": v.Token})
			resp, e = c.request(ctx(cmd), "POST", "/login", "application/json", bytes.NewReader(b), "")
			if e != nil {
				return e
			}
			resp.Body.Close()
			u, _ := url.Parse(c.Origin)
			for _, cookie := range jar.Cookies(u) {
				switch cookie.Name {
				case "aicrm_admin_session":
					credential.Session = cookie.Value
				case "aicrm_admin_csrf":
					credential.CSRF = cookie.Value
				}
			}
			if credential.Session == "" || credential.CSRF == "" {
				return fail("protocol", "login did not issue required session and CSRF", 1)
			}
			credential.Kind = "human"
		}
		if e = StoreCredential(credential); e != nil {
			return fail("credential_storage", "system keyring unavailable; no plaintext credential was saved", 1)
		}
		return o.print(map[string]any{"authenticated": true, "origin": c.Origin, "kind": credential.Kind, "credential_storage": "system_keyring"})
	}}
	login.Flags().StringVar(&username, "username", "", "existing human username")
	login.Flags().StringVar(&clientID, "client-id", "", "independent machine client ID")
	login.Flags().StringVar(&scope, "scope", "read", "requested narrowed token scope")
	login.Flags().BoolVar(&stdin, "secret-stdin", false, "read password or client secret from stdin")
	root.AddCommand(login)
	for _, name := range []string{"status", "whoami"} {
		root.AddCommand(&cobra.Command{Use: name, RunE: func(cmd *cobra.Command, _ []string) error {
			c, e := o.client()
			if e != nil {
				return e
			}
			path := "/open/v2/cli/context"
			if c.Credential.Kind == "human" {
				path = "/api/admin/access/cli/session"
			}
			return o.read(ctx(cmd), c, path)
		}})
	}
	root.AddCommand(&cobra.Command{Use: "logout", RunE: func(cmd *cobra.Command, _ []string) error {
		c, e := o.client()
		if e != nil {
			return e
		}
		if platformconfig.LoadCLIEnvironment().Token != "" {
			return o.print(map[string]any{"environment_token_requires_external_removal": true, "server_token_revoked": false})
		}
		var revokeErr error
		if c.Credential.Kind == "human" {
			resp, e := c.request(ctx(cmd), "POST", "/logout", "application/json", strings.NewReader("{}"), "")
			if e != nil {
				revokeErr = e
			} else {
				resp.Body.Close()
			}
		}
		if e = DeleteCredential(c.Origin); e != nil {
			return e
		}
		if revokeErr != nil {
			var f *Failure
			if errors.As(revokeErr, &f) {
				f.Result = map[string]any{"local_credential_removed": true, "server_session_revocation": "not_confirmed"}
			}
			return revokeErr
		}
		return o.print(map[string]any{"local_credential_removed": true, "server_session_revoked": c.Credential.Kind == "human", "machine_token_revocation": "use client disable or rotate"})
	}})
	can := &cobra.Command{Use: "can-i OPERATION_ID", Args: cobra.ExactArgs(1)}
	var resource string
	can.Flags().StringVar(&resource, "resource", "", "JSON resource file or -")
	can.RunE = func(cmd *cobra.Command, args []string) error {
		c, e := o.client()
		if e != nil {
			return e
		}
		r, e := readInput(o, resource)
		if e != nil {
			return e
		}
		input := map[string]any{"operation_id": args[0]}
		if len(r) > 0 {
			var v any
			if e = StrictJSON(r, &v); e != nil {
				return e
			}
			input["resource"] = v
		}
		b, _ := json.Marshal(input)
		path := "/open/v2/cli/can-i"
		if c.Credential.Kind == "human" {
			path = "/api/admin/cli/can-i"
		}
		resp, e := c.request(ctx(cmd), "POST", path, "application/json", bytes.NewReader(b), "")
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		var v map[string]any
		if e = json.NewDecoder(resp.Body).Decode(&v); e != nil {
			return e
		}
		if v["decision"] != "allowed" {
			return &Failure{Code: "permission_not_proven", Message: "resource permission denied or requires owner validation", Exit: 4, Result: v}
		}
		return o.print(v)
	}
	root.AddCommand(can)
	return root
}
