package mcptest

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/contractaxis"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"

	"github.com/mark3labs/mcp-go/mcp"
)

const (
	mcpContractAxisID            = "mcp-production-tool"
	readOnlyHintMutationFailure  = "live tools/list lost registration policy"
	starScopeMutationFailure     = "star scope no longer widens and narrows"
	writeGuardMutationFailure    = "member write reached an unguarded handler"
	contractAlphaWing            = "wing_alpha"
	contractBetaWing             = "wing_beta"
	contractSharedQuery          = "contract axis shared memory"
	contractUnknownSiblingSuffix = "__contract_axis_unknown"
)

type mcpContractTool struct {
	definition mcp.Tool
	catalog    mcpserver.CatalogEntry
	starScope  bool
}

// mcpContractFixture joins the two live authorities the adapter needs: the
// hosted tools/list schema and am_skillset's catalogue, both read over the same
// production HTTP edge. No tool-name manifest participates in the join.
type mcpContractFixture struct {
	t             *testing.T
	hosted        *Hosted
	admin         *Harness
	member        *Harness
	teamID        string
	tools         map[string]mcpContractTool
	names         []string
	fixtureSeeded bool
}

func newMCPContractFixture(t *testing.T, seedStarScope bool) *mcpContractFixture {
	t.Helper()
	ctx := context.Background()
	hosted := NewHosted(t)
	adminTenant, credential, err := hosted.Tenants.SeedTeamWithKey(
		ctx, "Contract Axis", "contract-axis", "contract-axis-admin@example.test",
	)
	if err != nil {
		t.Fatalf("seed contract-axis team: %v", err)
	}
	memberToken := addContractMember(t, hosted, adminTenant.TeamID)
	admin := hosted.Client(t, contractBetaWing, credential.Secret)
	member := hosted.Client(t, contractBetaWing, memberToken)

	fixture := &mcpContractFixture{
		t: t, hosted: hosted, admin: admin, member: member,
		teamID: adminTenant.TeamID,
	}
	if err := fixture.loadLiveTools(); err != nil {
		t.Fatalf("load live MCP contract class: %v", err)
	}
	if seedStarScope {
		alpha := hosted.Client(t, contractAlphaWing, credential.Secret)
		fixture.seedStarScope(alpha, admin)
		fixture.fixtureSeeded = true
	}
	return fixture
}

func addContractMember(t *testing.T, hosted *Hosted, teamID string) string {
	t.Helper()
	ctx := context.Background()
	email := "contract-axis-member@example.test"
	user, err := hosted.Tenants.CreateUserWithPassword(ctx, email, "mcptest-password", email)
	if err != nil {
		t.Fatalf("create contract-axis member: %v", err)
	}
	if _, err := hosted.Tenants.AddMemberByEmail(ctx, teamID, email, tenant.RoleMember); err != nil {
		t.Fatalf("add contract-axis member: %v", err)
	}
	token, err := hosted.Tenants.RevealToken(ctx, teamID, user.ID)
	if err != nil {
		t.Fatalf("reveal contract-axis member token: %v", err)
	}
	return token
}

func (f *mcpContractFixture) loadLiveTools() error {
	definitions, err := f.admin.ListToolDefinitions(f.t)
	if err != nil {
		return fmt.Errorf("tools/list: %w", err)
	}
	catalog, err := f.admin.ListCatalog(f.t)
	if err != nil {
		return fmt.Errorf("am_skillset catalogue: %w", err)
	}
	catalogByName := make(map[string]mcpserver.CatalogEntry, len(catalog))
	for _, entry := range catalog {
		if _, duplicate := catalogByName[entry.Name]; duplicate {
			return fmt.Errorf("catalogue contains duplicate %s", entry.Name)
		}
		catalogByName[entry.Name] = entry
	}

	f.tools = make(map[string]mcpContractTool, len(definitions))
	for _, definition := range definitions {
		entry, ok := catalogByName[definition.Name]
		if !ok {
			return fmt.Errorf("tools/list item %s has no catalogue entry", definition.Name)
		}
		delete(catalogByName, definition.Name)
		starScope, err := toolDeclaresStarScope(definition)
		if err != nil {
			return fmt.Errorf("%s star-scope schema: %w", definition.Name, err)
		}
		f.tools[definition.Name] = mcpContractTool{
			definition: definition,
			catalog:    entry,
			starScope:  starScope,
		}
		f.names = append(f.names, definition.Name)
	}
	if len(catalogByName) != 0 {
		var stale []string
		for name := range catalogByName {
			stale = append(stale, name)
		}
		sort.Strings(stale)
		return fmt.Errorf("catalogue entries missing from tools/list: %s", strings.Join(stale, ", "))
	}
	if len(f.names) == 0 {
		return fmt.Errorf("hosted tools/list returned an empty universe")
	}
	sort.Strings(f.names)
	return nil
}

func toolDeclaresStarScope(tool mcp.Tool) (bool, error) {
	raw, ok := tool.InputSchema.Properties["wing"]
	if !ok {
		return false, nil
	}
	property, ok := raw.(map[string]any)
	if !ok {
		return false, fmt.Errorf("wing property is %T, want a JSON Schema object", raw)
	}
	value, present := property[mcpprotocol.StarScopeSchemaExtension]
	if !present {
		return false, nil
	}
	declared, ok := value.(bool)
	if !ok || !declared {
		return false, fmt.Errorf("%s is %#v, want true", mcpprotocol.StarScopeSchemaExtension, value)
	}
	return true, nil
}

// TestMCPContractReadOnlyHintAssertion is the named assertion killed when the
// registrar stops publishing its actual add/addWrite policy in tools/list.
func TestMCPContractReadOnlyHintAssertion(t *testing.T) {
	fixture := newMCPContractFixture(t, false)
	if err := fixture.assertReadOnlyClass(); err != nil {
		contractMutationFatal(t, readOnlyHintMutationFailure, err)
	}
}

func (f *mcpContractFixture) assertReadOnlyClass() error {
	for _, name := range f.names {
		tool := f.tools[name]
		if tool.definition.Annotations.ReadOnlyHint == nil {
			return fmt.Errorf("%s has no readOnlyHint", name)
		}
		if got, want := *tool.definition.Annotations.ReadOnlyHint, !tool.catalog.Write; got != want {
			return fmt.Errorf("%s readOnlyHint=%t but live catalogue write=%t", name, got, tool.catalog.Write)
		}
	}
	return nil
}

// TestMCPContractInvokeAssertion proves every name discovered from hosted
// tools/list reaches the exact registered selector while an unknown sibling is
// rejected. The existing 0/41 lifecycle scenarios remain the independent
// business-effect evidence; this assertion owns only selector reachability.
func TestMCPContractInvokeAssertion(t *testing.T) {
	fixture := newMCPContractFixture(t, false)
	for _, name := range fixture.names {
		if err := fixture.probeInvoke(context.Background(), name, nil); err != nil {
			t.Fatal(err)
		}
	}
}

// TestMCPContractWriteGuardAssertion is the named assertion killed when
// addWrite publishes a write but skips the production role guard.
func TestMCPContractWriteGuardAssertion(t *testing.T) {
	fixture := newMCPContractFixture(t, false)
	if err := fixture.assertEveryMemberWriteRefused(); err != nil {
		contractMutationFatal(t, writeGuardMutationFailure, err)
	}
}

func (f *mcpContractFixture) assertEveryMemberWriteRefused() error {
	writes := 0
	for _, name := range f.names {
		if !f.tools[name].catalog.Write {
			continue
		}
		writes++
		if err := f.probeMemberRefusal(context.Background(), name, nil); err != nil {
			return err
		}
	}
	if writes == 0 {
		return fmt.Errorf("live catalogue contains no writes")
	}
	return nil
}

// TestMCPContractStarScopeAssertion is the named assertion killed when the
// searchWingFor selector no longer treats an explicit star as every wing.
func TestMCPContractStarScopeAssertion(t *testing.T) {
	fixture := newMCPContractFixture(t, true)
	if err := assertSearchWingSchemaOwnership(); err != nil {
		contractMutationFatal(t, starScopeMutationFailure, err)
	}
	starTools := 0
	for _, name := range fixture.names {
		if !fixture.tools[name].starScope {
			continue
		}
		starTools++
		if err := fixture.probeStarScope(context.Background(), name, nil); err != nil {
			contractMutationFatal(t, starScopeMutationFailure, err)
		}
	}
	if starTools == 0 {
		contractMutationFatal(t, starScopeMutationFailure, fmt.Errorf("tools/list declares no star-scoped wing property"))
	}
}

// assertSearchWingSchemaOwnership derives the expected class from production
// source structure: every registration function that calls searchWingFor must
// declare its wing through searchWingProperty, and the helper cannot be used by
// a handler that bypasses the resolver. This closes the otherwise invisible gap
// where removing a schema marker also removes the case from the live axis.
func assertSearchWingSchemaOwnership() error {
	entries, err := os.ReadDir(filepath.Join("..", "mcpserver"))
	if err != nil {
		return fmt.Errorf("read mcpserver source: %w", err)
	}
	fset := token.NewFileSet()
	matched := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join("..", "mcpserver", entry.Name())
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			hasResolver := containsCall(function.Body, "searchWingFor")
			hasProperty := containsCall(function.Body, "searchWingProperty")
			if hasResolver != hasProperty {
				return fmt.Errorf("%s has searchWingFor=%t searchWingProperty=%t", function.Name.Name, hasResolver, hasProperty)
			}
			if hasResolver {
				matched++
			}
		}
	}
	if matched == 0 {
		return fmt.Errorf("production source contains no searchWingFor registration")
	}
	return nil
}

func containsCall(node ast.Node, name string) bool {
	found := false
	ast.Inspect(node, func(candidate ast.Node) bool {
		call, ok := candidate.(*ast.CallExpr)
		if !ok {
			return !found
		}
		identifier, ok := call.Fun.(*ast.Ident)
		if ok && identifier.Name == name {
			found = true
			return false
		}
		return !found
	})
	return found
}

func contractMutationFatal(t *testing.T, reason string, err error) {
	t.Helper()
	t.Fatalf("%s: %v", contractaxis.MutationFailure(reason), err)
}

func (f *mcpContractFixture) seedStarScope(alpha, beta *Harness) {
	f.t.Helper()
	type seed struct {
		harness *Harness
		wing    string
		entity  string
		peer    string
	}
	for _, item := range []seed{
		{harness: alpha, wing: contractAlphaWing, entity: "AlphaAxisNode", peer: "AlphaAxisPeer"},
		{harness: beta, wing: contractBetaWing, entity: "BetaAxisNode", peer: "BetaAxisPeer"},
	} {
		rooms := []string{item.wing + "_decisions", item.wing + "_graph"}
		for index, room := range rooms {
			args := map[string]any{
				"room": room,
				"content": fmt.Sprintf("%s %s %s routes to %s. %s reports through %s.",
					contractSharedQuery, item.wing, item.entity, item.peer, item.entity, item.peer),
			}
			if index == 0 {
				args["code_anchors"] = []any{map[string]any{
					"path":    "internal/" + item.wing + "/marker.go",
					"snippet": "func " + item.entity + "Marker() { // " + item.wing,
				}}
			}
			item.harness.MustCall(f.t, "am_add_drawer", args)
		}
		item.harness.MustCall(f.t, "am_create_tunnel", map[string]any{
			"source_wing": item.wing, "source_room": rooms[0],
			"target_wing": item.wing, "target_room": rooms[1],
			"label": item.wing + " contract axis tunnel",
		})
		item.harness.MustCall(f.t, "am_search", map[string]any{
			"query": item.wing + " unanswered contract axis query",
			"room":  item.wing + "_missing",
		})
	}
	beta.MustCall(f.t, "am_recompute_graph", map[string]any{})
}

func (f *mcpContractFixture) probeInvoke(_ context.Context, name string, observation *contractaxis.Observation) error {
	if _, ok := f.tools[name]; !ok {
		return fmt.Errorf("%s is absent from the live tools/list universe", name)
	}
	_, _, err := f.admin.Call(f.t, name, map[string]any{})
	if err != nil {
		return fmt.Errorf("invoke exact registered name %s: %w", name, err)
	}
	if observation != nil {
		observation.RecordBinding()
		observation.RecordPositive()
	}

	sibling := name + contractUnknownSiblingSuffix
	_, isError, siblingErr := f.admin.Call(f.t, sibling, map[string]any{})
	if siblingErr == nil && !isError {
		return fmt.Errorf("unknown sibling %s was accepted", sibling)
	}
	if observation != nil {
		observation.RecordNegative()
	}
	return nil
}

func (f *mcpContractFixture) probeMemberRefusal(_ context.Context, name string, observation *contractaxis.Observation) error {
	tool, ok := f.tools[name]
	if !ok || !tool.catalog.Write {
		return fmt.Errorf("%s is not a live catalogue write", name)
	}
	beforeState, err := f.hosted.DurableStateDigest(context.Background())
	if err != nil {
		return fmt.Errorf("digest state before %s: %w", name, err)
	}
	beforeUsage, err := f.usageUsed()
	if err != nil {
		return fmt.Errorf("usage before %s: %w", name, err)
	}
	out, isError, err := f.member.Call(f.t, name, map[string]any{})
	if err != nil {
		return fmt.Errorf("member invoke %s: %w", name, err)
	}
	if !isError {
		return fmt.Errorf("member invoke %s succeeded: %s", name, out)
	}
	if observation != nil {
		observation.RecordBinding()
	}
	if !strings.Contains(out, name+" changes stored memory") || !strings.Contains(out, "read-only") {
		return fmt.Errorf("%s bypassed the production writeGuard wording: %s", name, out)
	}
	if observation != nil {
		observation.RecordPositive()
	}
	afterState, err := f.hosted.DurableStateDigest(context.Background())
	if err != nil {
		return fmt.Errorf("digest state after %s: %w", name, err)
	}
	afterUsage, err := f.usageUsed()
	if err != nil {
		return fmt.Errorf("usage after %s: %w", name, err)
	}
	if afterState != beforeState {
		return fmt.Errorf("member-refused %s changed durable state", name)
	}
	if afterUsage != beforeUsage {
		return fmt.Errorf("member-refused %s changed usage from %d to %d", name, beforeUsage, afterUsage)
	}
	if observation != nil {
		observation.RecordNegative()
	}
	return nil
}

func (f *mcpContractFixture) usageUsed() (int, error) {
	snapshot, err := f.hosted.Usage.Snapshot(context.Background(), f.teamID)
	if err != nil {
		return 0, err
	}
	return snapshot.Used, nil
}

func (f *mcpContractFixture) probeStarScope(_ context.Context, name string, observation *contractaxis.Observation) error {
	tool, ok := f.tools[name]
	if !ok || !tool.starScope {
		return fmt.Errorf("%s is not a live star-scope schema item", name)
	}
	if !f.fixtureSeeded {
		return fmt.Errorf("star-scope fixture was not seeded")
	}
	wideArgs, err := contractArguments(tool.definition)
	if err != nil {
		return fmt.Errorf("arguments for %s: %w", name, err)
	}
	wideArgs["wing"] = "*"
	wide, isError, err := f.admin.Call(f.t, name, wideArgs)
	if err != nil {
		return fmt.Errorf("%s wing=* transport: %w", name, err)
	}
	if isError {
		return fmt.Errorf("%s wing=* tool error: %s", name, wide)
	}
	if observation != nil {
		observation.RecordBinding()
	}
	if !strings.Contains(wide, contractAlphaWing) || !strings.Contains(wide, contractBetaWing) {
		return fmt.Errorf("%s wing=* did not include both seeded wings: %s", name, truncateContractOutput(wide))
	}
	if observation != nil {
		observation.RecordPositive()
	}

	narrowArgs, err := contractArguments(tool.definition)
	if err != nil {
		return fmt.Errorf("narrow arguments for %s: %w", name, err)
	}
	narrow, isError, err := f.admin.Call(f.t, name, narrowArgs)
	if err != nil {
		return fmt.Errorf("%s omitted wing transport: %w", name, err)
	}
	if isError {
		return fmt.Errorf("%s omitted wing tool error: %s", name, narrow)
	}
	if !strings.Contains(narrow, contractBetaWing) || strings.Contains(narrow, contractAlphaWing) {
		return fmt.Errorf("%s omitted wing was not narrowed to its registration: %s", name, truncateContractOutput(narrow))
	}
	if observation != nil {
		observation.RecordNegative()
	}
	return nil
}

// contractArguments satisfies required schema properties generically. The
// adapter has no item-name switch: if another live tool declares star scope, it
// immediately receives the same two-wing observation.
func contractArguments(tool mcp.Tool) (map[string]any, error) {
	arguments := map[string]any{}
	for _, name := range tool.InputSchema.Required {
		raw, ok := tool.InputSchema.Properties[name]
		if !ok {
			return nil, fmt.Errorf("required property %s has no schema", name)
		}
		property, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("required property %s is %T", name, raw)
		}
		switch property["type"] {
		case "string":
			arguments[name] = contractSharedQuery
		case "number", "integer":
			arguments[name] = 1
		case "boolean":
			arguments[name] = true
		case "array":
			arguments[name] = []any{}
		case "object":
			arguments[name] = map[string]any{}
		default:
			return nil, fmt.Errorf("required property %s has unsupported type %#v", name, property["type"])
		}
	}
	if _, ok := tool.InputSchema.Properties["limit"]; ok {
		arguments["limit"] = 100
	}
	if _, ok := tool.InputSchema.Properties["unanswered"]; ok {
		arguments["unanswered"] = 100
	}
	if _, ok := tool.InputSchema.Properties["hours"]; ok {
		arguments["hours"] = 1
	}
	if _, ok := tool.InputSchema.Properties["max_distance"]; ok {
		arguments["max_distance"] = 0
	}
	return arguments, nil
}

func truncateContractOutput(output string) string {
	const limit = 600
	if len(output) <= limit {
		return output
	}
	return output[:limit] + "..."
}

func (f *mcpContractFixture) axis(ctx context.Context) contractaxis.Axis {
	return contractaxis.Axis{
		ID:       mcpContractAxisID,
		Maturity: contractaxis.Enforced,
		Universe: func(context.Context) ([]string, error) {
			if err := f.assertReadOnlyClass(); err != nil {
				return nil, err
			}
			if err := assertSearchWingSchemaOwnership(); err != nil {
				return nil, err
			}
			return append([]string(nil), f.names...), nil
		},
		Cases: func(_ context.Context, item string) ([]string, error) {
			tool, ok := f.tools[item]
			if !ok {
				return nil, fmt.Errorf("%s disappeared from the live universe", item)
			}
			cases := []string{"invoke"}
			if tool.catalog.Write {
				cases = append(cases, "member-refuse")
			}
			if tool.starScope {
				cases = append(cases, "star-scope")
			}
			return cases, nil
		},
		Probe: func(ctx context.Context, item, caseID string, observation *contractaxis.Observation) error {
			switch caseID {
			case "invoke":
				return f.probeInvoke(ctx, item, observation)
			case "member-refuse":
				return f.probeMemberRefusal(ctx, item, observation)
			case "star-scope":
				return f.probeStarScope(ctx, item, observation)
			default:
				return fmt.Errorf("unknown MCP contract case %q", caseID)
			}
		},
	}
}
