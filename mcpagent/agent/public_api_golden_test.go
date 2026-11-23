package mcpagent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// This is the completed migration ratchet for the Agent surface. It started at
// 70 methods and now pins the final Agent/Session API exactly. Any deliberate
// change updates this list in the same commit.
//
// SetInstalledSkillResolver was added deliberately: read_skill can serve a
// skill the host installed but did not attach, and the host only knows its
// workspace path after construction, so RuntimeConfig could not carry it
// without a new parameter on a 25-argument constructor and every call site.
func TestAgentPublicMethodSurface(t *testing.T) {
	want := []string{"Close", "Definition", "Run", "SetInstalledSkillResolver", "Start"}

	typeOfAgent := reflect.TypeOf((*Agent)(nil))
	got := make([]string, 0, typeOfAgent.NumMethod())
	for i := 0; i < typeOfAgent.NumMethod(); i++ {
		got = append(got, typeOfAgent.Method(i).Name)
	}

	if len(got) != 5 {
		t.Fatalf("exported *Agent method count = %d, want final surface 5; methods=%v", len(got), got)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("exported *Agent methods changed\n got: %v\nwant: %v", got, want)
	}
}

func TestAgentHasNoExportedFields(t *testing.T) {
	typeOfAgent := reflect.TypeOf(Agent{})
	var exported []string
	for i := 0; i < typeOfAgent.NumField(); i++ {
		field := typeOfAgent.Field(i)
		if field.IsExported() {
			exported = append(exported, field.Name)
		}
	}
	if len(exported) != 0 {
		t.Fatalf("Agent exposes mutable fields: %v", exported)
	}
}

func TestAgentAndSessionCloseContractsMatch(t *testing.T) {
	errorType := reflect.TypeOf((*error)(nil)).Elem()
	for _, target := range []struct {
		name string
		typ  reflect.Type
	}{
		{name: "Agent", typ: reflect.TypeOf((*Agent)(nil))},
		{name: "Session", typ: reflect.TypeOf((*Session)(nil))},
	} {
		method, ok := target.typ.MethodByName("Close")
		if !ok || method.Type.NumOut() != 1 || method.Type.Out(0) != errorType {
			t.Fatalf("%s.Close must return exactly error; method=%v", target.name, method)
		}
	}
}

func TestPackageFunctionSurfaceDoesNotRegrow(t *testing.T) {
	want := []string{
		"AgentSupportsSteering",
		"ApplyAgentResumeHandle",
		"BuildClaudeNativeQuestionSettings",
		"BuildSafeEnvironment",
		"ClearHTTPSessionStopped",
		"ClearSessionsStopped",
		"CloseAllSessions",
		"CloseHTTPSession",
		"CloseSession",
		"CloseSessionServer",
		"ConvertToolChoice",
		"DeliverAgentControlKey",
		"DeliverAgentInput",
		"GetAllSessionStats",
		"GetDefaultMaxTurns",
		"GetSessionConnections",
		"GetSessionRegistry",
		"GetSessionStats",
		"HandleLoopDetection",
		"HasSession",
		"InvokeAgentVirtualTool",
		"IsBrokenPipeError",
		"ListSessions",
		"LookupSession",
		"MarkSessionsStopped",
		"NewAgentConnectionWithSession",
		"NewAgentFromDefinition",
		"NewFileCodingSessionStore",
		"NewMemoryCodingSessionStore",
		"NewStreamingTracer",
		"NewToolFilter",
		"NewToolLoopDetector",
		"ReadAgentDiagnostics",
		"ReadAgentRuntimeInfo",
		"ReadAgentSystemPrompt",
		"RegisterHTTPSession",
		"RemoveIsolatedSessionWorkspace",
		"ResolveConnectionSessionID",
		"SetAccountPhraseResolver",
		"SnapshotAgentSession",
		"StartAgentTransportSession",
		"SubscribeAgentEvents",
	}
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, ".", func(info fs.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg := packages["mcpagent"]
	if pkg == nil {
		t.Fatal("mcpagent package not found")
	}
	var exported []string
	for _, file := range pkg.Files {
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() {
				continue
			}
			exported = append(exported, fn.Name.Name)
			if strings.HasSuffix(fn.Name.Name, "ForTesting") {
				t.Fatalf("test-only helper leaked into production API: %s", fn.Name.Name)
			}
		}
	}
	slices.Sort(exported)
	if !slices.Equal(exported, want) {
		t.Fatalf("exported package function inventory changed\n got (%d): %v\nwant (%d): %v", len(exported), exported, len(want), want)
	}
}

func TestAgentFacadeFunctionSurface(t *testing.T) {
	want := []string{
		"AgentSupportsSteering",
		"ApplyAgentResumeHandle",
		"DeliverAgentControlKey",
		"DeliverAgentInput",
		"HandleLoopDetection",
		"InvokeAgentVirtualTool",
		"NewAgentFromDefinition",
		"ReadAgentDiagnostics",
		"ReadAgentRuntimeInfo",
		"ReadAgentSystemPrompt",
		"SnapshotAgentSession",
		"StartAgentTransportSession",
		"SubscribeAgentEvents",
	}

	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, ".", func(info fs.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg := packages["mcpagent"]
	if pkg == nil {
		t.Fatal("mcpagent package not found")
	}
	var got []string
	for _, file := range pkg.Files {
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() {
				continue
			}
			if fieldListMentionsAgent(fn.Type.Params) || fieldListMentionsAgent(fn.Type.Results) {
				got = append(got, fn.Name.Name)
			}
		}
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("exported Agent facade changed\n got (%d): %v\nwant (%d): %v", len(got), got, len(want), want)
	}
}

func fieldListMentionsAgent(fields *ast.FieldList) bool {
	if fields == nil {
		return false
	}
	for _, field := range fields.List {
		if expressionMentionsAgent(field.Type) {
			return true
		}
	}
	return false
}

func expressionMentionsAgent(expression ast.Expr) bool {
	switch value := expression.(type) {
	case *ast.StarExpr:
		identifier, ok := value.X.(*ast.Ident)
		return ok && identifier.Name == "Agent"
	case *ast.ArrayType:
		return expressionMentionsAgent(value.Elt)
	case *ast.MapType:
		return expressionMentionsAgent(value.Key) || expressionMentionsAgent(value.Value)
	case *ast.ChanType:
		return expressionMentionsAgent(value.Value)
	case *ast.Ellipsis:
		return expressionMentionsAgent(value.Elt)
	case *ast.FuncType:
		return fieldListMentionsAgent(value.Params) || fieldListMentionsAgent(value.Results)
	case *ast.IndexExpr:
		return expressionMentionsAgent(value.X) || expressionMentionsAgent(value.Index)
	case *ast.IndexListExpr:
		if expressionMentionsAgent(value.X) {
			return true
		}
		for _, index := range value.Indices {
			if expressionMentionsAgent(index) {
				return true
			}
		}
	}
	return false
}

// ActiveTurnID was added deliberately (PLAT-180): a host tagging per-tool-call
// events with a turn ID must ask the session which turn is active RIGHT NOW,
// not cache a turn ID captured once at an earlier setup -- a retained
// (tmux-delivered) turn never calls Run again, so a cached ID goes stale the
// moment Send starts a new retained turn.
func TestSessionPublicMethodSurface(t *testing.T) {
	want := []string{"ActiveTurnID", "Close", "Events", "ObserveNativeInput", "PrepareNativeInterrupt", "Run", "Send", "Snapshot"}
	typeOfSession := reflect.TypeOf((*Session)(nil))
	got := make([]string, 0, typeOfSession.NumMethod())
	for i := 0; i < typeOfSession.NumMethod(); i++ {
		got = append(got, typeOfSession.Method(i).Name)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("exported *Session methods changed\n got: %v\nwant: %v", got, want)
	}
}
