package codex

// Conformance of the hand-written protocol types (protocol.go) with the JSON
// schema generated from the pinned binary (schema/). When Codex is upgraded,
// regenerate the schema (see docs/providers/codex.md) and run this test: any
// method, field, or enum value the adapter relies on that disappeared or was
// renamed fails here instead of silently at runtime.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type schemaIndex struct {
	defs map[string]map[string]any
}

func loadSchema(t *testing.T) *schemaIndex {
	t.Helper()
	idx := &schemaIndex{defs: map[string]map[string]any{}}
	add := func(defs map[string]any) {
		for k, v := range defs {
			if m, ok := v.(map[string]any); ok {
				if _, seen := idx.defs[k]; !seen {
					idx.defs[k] = m
				}
			}
		}
	}
	for _, f := range []string{"codex_app_server_protocol.v2.schemas.json", "codex_app_server_protocol.schemas.json", "ClientRequest.json", "ServerRequest.json", "ServerNotification.json"} {
		root := readJSON(t, f)
		defs, _ := root["definitions"].(map[string]any)
		add(defs)
		if v2, ok := defs["v2"].(map[string]any); ok {
			add(v2)
		}
	}
	// Top-level response/params files are definitions in their own right.
	entries, err := os.ReadDir("schema")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") && !strings.HasPrefix(e.Name(), "codex_app_server_protocol") {
			name := strings.TrimSuffix(e.Name(), ".json")
			if _, seen := idx.defs[name]; !seen {
				idx.defs[name] = readJSON(t, e.Name())
			}
		}
	}
	return idx
}

func readJSON(t *testing.T, name string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("schema", name))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return m
}

func (x *schemaIndex) resolve(s map[string]any) map[string]any {
	if ref, ok := s["$ref"].(string); ok {
		name := ref[strings.LastIndex(ref, "/")+1:]
		return x.defs[name]
	}
	return s
}

// props collects property schemas across $ref/oneOf/anyOf/allOf.
func (x *schemaIndex) props(s map[string]any, depth int) map[string]map[string]any {
	out := map[string]map[string]any{}
	if s == nil || depth > 6 {
		return out
	}
	s = x.resolve(s)
	if s == nil {
		return out
	}
	if p, ok := s["properties"].(map[string]any); ok {
		for k, v := range p {
			vm, _ := v.(map[string]any)
			out[k] = vm
		}
	}
	for _, key := range []string{"oneOf", "anyOf", "allOf"} {
		if list, ok := s[key].([]any); ok {
			for _, it := range list {
				if m, ok := it.(map[string]any); ok {
					for k, v := range x.props(m, depth+1) {
						out[k] = v
					}
				}
			}
		}
	}
	return out
}

// enums collects string enum values across $ref/oneOf/anyOf/allOf.
func (x *schemaIndex) enums(s map[string]any, depth int) map[string]bool {
	out := map[string]bool{}
	if s == nil || depth > 6 {
		return out
	}
	s = x.resolve(s)
	if s == nil {
		return out
	}
	if list, ok := s["enum"].([]any); ok {
		for _, v := range list {
			if str, ok := v.(string); ok {
				out[str] = true
			}
		}
	}
	for _, key := range []string{"oneOf", "anyOf", "allOf"} {
		if list, ok := s[key].([]any); ok {
			for _, it := range list {
				if m, ok := it.(map[string]any); ok {
					for k := range x.enums(m, depth+1) {
						out[k] = true
					}
				}
			}
		}
	}
	return out
}

func methods(t *testing.T, file string) map[string]bool {
	out := map[string]bool{}
	for _, v := range readJSON(t, file)["oneOf"].([]any) {
		m := v.(map[string]any)["properties"].(map[string]any)["method"].(map[string]any)
		out[m["enum"].([]any)[0].(string)] = true
	}
	return out
}

func TestSchemaMethods(t *testing.T) {
	client := methods(t, "ClientRequest.json")
	server := methods(t, "ServerRequest.json")
	notes := methods(t, "ServerNotification.json")
	clientNotes := methods(t, "ClientNotification.json")
	check := func(set map[string]bool, kind string, ms ...string) {
		for _, m := range ms {
			if !set[m] {
				t.Errorf("%s %q is not in the pinned schema", kind, m)
			}
		}
	}
	check(client, "client request", methodInitialize, methodThreadStart, methodThreadResume, methodTurnStart,
		methodTurnSteer, methodTurnInterrupt, methodAccountRead, methodRateLimitsRead, methodModelList,
		methodConfigRead, methodHooksList)
	check(clientNotes, "client notification", notifyInitialized)
	check(server, "server request", reqCommandApproval, reqFileChangeApproval, reqPermissions, reqUserInput,
		reqElicitation, reqDynamicToolCall, reqAuthRefresh, reqAttestation, reqLegacyApplyPatch, reqLegacyExecCommand)
	check(notes, "server notification", ntfError, ntfThreadStarted, ntfThreadStatus, ntfTokenUsage, ntfTurnStarted,
		ntfTurnCompleted, ntfTurnPlan, ntfItemStarted, ntfItemCompleted, ntfAgentDelta, ntfRequestResolved,
		ntfMCPStartup, ntfRateLimits, ntfWarning, ntfConfigWarning, ntfGuardianWarning, ntfDeprecation,
		ntfHookStarted, ntfModelRerouted, ntfAccountUpdated, ntfThreadClosed, ntfModelVerification)

	// Every server request in the pinned schema must have an explicit
	// handler (never leave a request unanswered).
	handled := map[string]bool{reqCommandApproval: true, reqFileChangeApproval: true, reqPermissions: true,
		reqUserInput: true, reqElicitation: true, reqDynamicToolCall: true, reqAuthRefresh: true,
		reqAttestation: true, reqLegacyApplyPatch: true, reqLegacyExecCommand: true}
	var missing []string
	for m := range server {
		if !handled[m] {
			missing = append(missing, m)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("server requests without an explicit mapping: %v", missing)
	}
}

func TestSchemaFields(t *testing.T) {
	x := loadSchema(t)
	pairs := []struct {
		v    any
		defs []string
	}{
		{initializeParams{}, []string{"InitializeParams"}},
		{clientInfo{}, []string{"ClientInfo"}},
		{initializeCapabilities{}, []string{"InitializeCapabilities"}},
		{initializeResponse{}, []string{"InitializeResponse"}},
		{getAccountParams{}, []string{"GetAccountParams"}},
		{account{}, []string{"Account"}},
		{getAccountResponse{}, []string{"GetAccountResponse"}},
		{rateLimitWindow{}, []string{"RateLimitWindow"}},
		{rateLimitSnapshot{}, []string{"RateLimitSnapshot"}},
		{accountRateLimitsUpdated{}, []string{"AccountRateLimitsUpdatedNotification"}},
		{getAccountRateLimitsResponse{}, []string{"GetAccountRateLimitsResponse"}},
		{modelListParams{}, []string{"ModelListParams"}},
		{modelInfo{}, []string{"Model"}},
		{modelListResponse{}, []string{"ModelListResponse"}},
		{configReadParams{}, []string{"ConfigReadParams"}},
		{configLayerSource{}, []string{"ConfigLayerSource"}},
		{configLayer{}, []string{"ConfigLayer"}},
		{configReadResponse{}, []string{"ConfigReadResponse"}},
		{hooksListParams{}, []string{"HooksListParams"}},
		{hookMetadata{}, []string{"HookMetadata"}},
		{hooksListEntry{}, []string{"HooksListEntry"}},
		{hooksListResponse{}, []string{"HooksListResponse"}},
		{threadParams{}, []string{"ThreadResumeParams"}},
		{sandboxPolicy{}, []string{"SandboxPolicy"}},
		{thread{}, []string{"Thread"}},
		{threadStartResponse{}, []string{"ThreadStartResponse", "ThreadResumeResponse"}},
		{userInput{}, []string{"UserInput"}},
		{turnStartParams{}, []string{"TurnStartParams"}},
		{turnError{}, []string{"TurnError"}},
		{turn{}, []string{"Turn"}},
		{turnStartResponse{}, []string{"TurnStartResponse"}},
		{turnSteerParams{}, []string{"TurnSteerParams"}},
		{turnSteerResponse{}, []string{"TurnSteerResponse"}},
		{turnInterruptParams{}, []string{"TurnInterruptParams"}},
		{turnNotification{}, []string{"TurnStartedNotification", "TurnCompletedNotification"}},
		{errorNotification{}, []string{"ErrorNotification"}},
		{fileUpdateChange{}, []string{"FileUpdateChange"}},
		{mcpToolCallError{}, []string{"McpToolCallError"}},
		{threadItem{}, []string{"ThreadItem"}},
		{itemNotification{}, []string{"ItemStartedNotification", "ItemCompletedNotification"}},
		{agentMessageDelta{}, []string{"AgentMessageDeltaNotification"}},
		{tokenUsageBreakdown{}, []string{"TokenUsageBreakdown"}},
		{tokenUsageUpdated{}, []string{"ThreadTokenUsageUpdatedNotification"}},
		{planUpdated{}, []string{"TurnPlanUpdatedNotification"}},
		{warningNotification{}, []string{"WarningNotification"}},
		{configWarningNotification{}, []string{"ConfigWarningNotification"}},
		{mcpStartupNotification{}, []string{"McpServerStatusUpdatedNotification"}},
		{hookStartedNotification{}, []string{"HookStartedNotification"}},
		{modelReroutedNotification{}, []string{"ModelReroutedNotification"}},
		{threadStatusNotification{}, []string{"ThreadStatusChangedNotification"}},
		{requestResolvedNotification{}, []string{"ServerRequestResolvedNotification"}},
		{commandAction{}, []string{"CommandAction"}},
		{networkApprovalContext{}, []string{"NetworkApprovalContext"}},
		{commandApprovalParams{}, []string{"CommandExecutionRequestApprovalParams"}},
		{fileChangeApprovalParams{}, []string{"FileChangeRequestApprovalParams"}},
		{approvalResponse{}, []string{"CommandExecutionRequestApprovalResponse", "FileChangeRequestApprovalResponse", "ExecCommandApprovalResponse", "ApplyPatchApprovalResponse"}},
		{fileSystemPermissions{}, []string{"AdditionalFileSystemPermissions"}},
		{networkPermissions{}, []string{"AdditionalNetworkPermissions"}},
		{permissionProfile{}, []string{"RequestPermissionProfile", "GrantedPermissionProfile"}},
		{permissionsApprovalParams{}, []string{"PermissionsRequestApprovalParams"}},
		{permissionsApprovalResponse{}, []string{"PermissionsRequestApprovalResponse"}},
		{userInputOption{}, []string{"ToolRequestUserInputOption"}},
		{userInputQuestion{}, []string{"ToolRequestUserInputQuestion"}},
		{userInputParams{}, []string{"ToolRequestUserInputParams"}},
		{userInputAnswer{}, []string{"ToolRequestUserInputAnswer"}},
		{userInputResponse{}, []string{"ToolRequestUserInputResponse"}},
		{elicitationParams{}, []string{"McpServerElicitationRequestParams"}},
		{elicitationResponse{}, []string{"McpServerElicitationRequestResponse"}},
		{dynamicToolContentItem{}, []string{"DynamicToolCallOutputContentItem"}},
		{dynamicToolCallResponse{}, []string{"DynamicToolCallResponse"}},
		{legacyExecApprovalParams{}, []string{"ExecCommandApprovalParams"}},
		{legacyPatchApprovalParams{}, []string{"ApplyPatchApprovalParams"}},
	}
	for _, p := range pairs {
		rt := reflect.TypeOf(p.v)
		for _, def := range p.defs {
			s, ok := x.defs[def]
			if !ok {
				t.Errorf("%s: schema definition %s not found", rt.Name(), def)
				continue
			}
			props := x.props(s, 0)
			for i := 0; i < rt.NumField(); i++ {
				tag := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
				if tag == "" || tag == "-" {
					continue
				}
				if _, ok := props[tag]; !ok {
					t.Errorf("%s.%s: field %q not in schema %s", rt.Name(), rt.Field(i).Name, tag, def)
				}
			}
		}
	}
	// thread/start sends every threadParams field except threadId (resume).
	start := x.props(x.defs["ThreadStartParams"], 0)
	// Guard against a vacuous check (e.g. definitions failing to load).
	if len(x.defs) < 200 || start["developerInstructions"] == nil || start["noSuchField"] != nil || len(x.props(x.defs["ThreadItem"], 0)) < 20 {
		t.Fatalf("schema index looks wrong: %d defs", len(x.defs))
	}
	rt := reflect.TypeOf(threadParams{})
	for i := 0; i < rt.NumField(); i++ {
		tag := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
		if _, ok := start[tag]; !ok && tag != "threadId" {
			t.Errorf("threadParams.%s not accepted by thread/start", tag)
		}
	}
}

func TestSchemaEnums(t *testing.T) {
	x := loadSchema(t)
	check := func(def string, values ...string) {
		t.Helper()
		got := x.enums(x.defs[def], 0)
		for _, v := range values {
			if !got[v] {
				t.Errorf("%s does not accept %q (has %v)", def, v, got)
			}
		}
	}
	if got := x.enums(x.defs["SandboxMode"], 0); len(got) != 3 || got["bogus"] {
		t.Fatalf("SandboxMode enum index looks wrong: %v", got)
	}
	check("SandboxMode", sandboxReadOnly, sandboxWorkspaceWrite)
	check("ApprovalsReviewer", approvalsReviewerUser)
	check("TurnStatus", turnCompleted, turnInterrupted, turnFailed, turnInProgress)
	check("CommandExecutionApprovalDecision", decisionAccept, decisionDecline)
	check("FileChangeApprovalDecision", decisionAccept, decisionDecline)
	check("ReviewDecision", "approved")
	check("CodexErrorInfo", errUsageLimitExceeded, errUnauthorized, errServerOverloaded)
	check("HookTrustStatus", hookTrustManaged, hookTrustTrusted)
	check("McpServerElicitationAction", "accept", "decline")
	check("PermissionGrantScope", "turn")
	check("CommandExecutionStatus", "completed", "failed", "declined")
	check("PatchApplyStatus", "completed", "failed", "declined")
	check("McpServerStartupState", "failed", "starting", "ready")

	// Approval policies used for edit and read-only runs.
	check("AskForApproval", approvalPolicy("edit").(string), approvalPolicy("readonly").(string))
	// Account variants used by billing classification.
	acct := map[string]bool{}
	for _, v := range x.defs["Account"]["oneOf"].([]any) {
		for k := range x.enums(v.(map[string]any)["properties"].(map[string]any)["type"].(map[string]any), 0) {
			acct[k] = true
		}
	}
	for _, v := range []string{accountAPIKey, accountChatGPT, accountBedrock} {
		if !acct[v] {
			t.Errorf("Account type %q not in schema", v)
		}
	}
}
