package cli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

func TestParseArgsOpen(t *testing.T) {
	cmd, err := ParseArgs([]string{"open", "http://localhost:3000"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Name != "open" {
		t.Errorf("expected cmd Name 'open', got %q", cmd.Name)
	}
	if cmd.Method != protocol.MethodOpen {
		t.Errorf("expected Method %q, got %q", protocol.MethodOpen, cmd.Method)
	}
	params, ok := cmd.Params.(protocol.OpenParams)
	if !ok {
		t.Fatalf("expected OpenParams, got %T", cmd.Params)
	}
	if params.URL != "http://localhost:3000" {
		t.Errorf("expected URL 'http://localhost:3000', got %q", params.URL)
	}
}

func TestParseArgsSnapshot(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected protocol.SnapshotParams
	}{
		{
			name:     "bare snapshot",
			args:     []string{"snapshot"},
			expected: protocol.SnapshotParams{},
		},
		{
			name: "interactive flag short",
			args: []string{"snapshot", "-i"},
			expected: protocol.SnapshotParams{
				InteractiveOnly: true,
			},
		},
		{
			name: "interactive and compact long",
			args: []string{"snapshot", "--interactive", "--compact"},
			expected: protocol.SnapshotParams{
				InteractiveOnly: true,
				Compact:         true,
			},
		},
		{
			name: "depth and selector flags",
			args: []string{"snapshot", "-i", "-c", "-d", "4", "-s", "#main-nav"},
			expected: protocol.SnapshotParams{
				InteractiveOnly: true,
				Compact:         true,
				MaxDepth:        4,
				Selector:        "#main-nav",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := ParseArgs(tt.args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cmd.Method != protocol.MethodSnapshot {
				t.Errorf("expected Method %q, got %q", protocol.MethodSnapshot, cmd.Method)
			}
			params, ok := cmd.Params.(protocol.SnapshotParams)
			if !ok {
				t.Fatalf("expected SnapshotParams, got %T", cmd.Params)
			}
			if !reflect.DeepEqual(params, tt.expected) {
				t.Errorf("expected %+v, got %+v", tt.expected, params)
			}
		})
	}
}

func TestParseArgsClickAndDblClick(t *testing.T) {
	// click
	cmd, err := ParseArgs([]string{"click", "@e12"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Method != protocol.MethodClick {
		t.Errorf("expected Method %q, got %q", protocol.MethodClick, cmd.Method)
	}
	params := cmd.Params.(protocol.ClickParams)
	if params.Selector != "@e12" {
		t.Errorf("expected selector '@e12', got %q", params.Selector)
	}

	// dblclick
	cmd2, err := ParseArgs([]string{"dblclick", "button.submit"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd2.Method != protocol.MethodDblClick {
		t.Errorf("expected Method %q, got %q", protocol.MethodDblClick, cmd2.Method)
	}
	params2 := cmd2.Params.(protocol.ClickParams)
	if params2.Selector != "button.submit" || params2.ClickCount != 2 {
		t.Errorf("expected clickCount 2 on button.submit, got %+v", params2)
	}
}

func TestParseArgsFillAndType(t *testing.T) {
	// fill
	cmd, err := ParseArgs([]string{"fill", "@e5", "jane.doe@example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Method != protocol.MethodFill {
		t.Errorf("expected Method %q, got %q", protocol.MethodFill, cmd.Method)
	}
	fillParams := cmd.Params.(protocol.FillParams)
	if fillParams.Selector != "@e5" || fillParams.Text != "jane.doe@example.com" {
		t.Errorf("unexpected fill params: %+v", fillParams)
	}

	// type with multiple words
	cmd2, err := ParseArgs([]string{"type", "input#search", "search", "query", "here"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd2.Method != protocol.MethodType {
		t.Errorf("expected Method %q, got %q", protocol.MethodType, cmd2.Method)
	}
	typeParams := cmd2.Params.(protocol.TypeParams)
	if typeParams.Selector != "input#search" || typeParams.Text != "search query here" {
		t.Errorf("unexpected type params: %+v", typeParams)
	}
}

func TestParseArgsPressHoverFocus(t *testing.T) {
	// press
	cmd, err := ParseArgs([]string{"press", "Enter"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Method != protocol.MethodPress {
		t.Errorf("expected Method %q, got %q", protocol.MethodPress, cmd.Method)
	}
	pressParams := cmd.Params.(protocol.PressParams)
	if pressParams.Key != "Enter" {
		t.Errorf("expected key 'Enter', got %q", pressParams.Key)
	}

	// hover
	cmd2, err := ParseArgs([]string{"hover", "@e9"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd2.Method != protocol.MethodHover {
		t.Errorf("expected Method %q, got %q", protocol.MethodHover, cmd2.Method)
	}
	hoverParams := cmd2.Params.(protocol.HoverParams)
	if hoverParams.Selector != "@e9" {
		t.Errorf("expected selector '@e9', got %q", hoverParams.Selector)
	}

	// focus
	cmd3, err := ParseArgs([]string{"focus", "#name-input"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd3.Method != protocol.MethodFocus {
		t.Errorf("expected Method %q, got %q", protocol.MethodFocus, cmd3.Method)
	}
	focusParams := cmd3.Params.(protocol.FocusParams)
	if focusParams.Selector != "#name-input" {
		t.Errorf("expected selector '#name-input', got %q", focusParams.Selector)
	}
}

func TestParseArgsEvalAndWait(t *testing.T) {
	// eval
	cmd, err := ParseArgs([]string{"eval", "document.title"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Method != protocol.MethodEval {
		t.Errorf("expected Method %q, got %q", protocol.MethodEval, cmd.Method)
	}
	evalParams := cmd.Params.(protocol.EvalParams)
	if evalParams.Expression != "document.title" || !evalParams.AwaitPromise {
		t.Errorf("unexpected eval params: %+v", evalParams)
	}

	// wait by milliseconds
	// wait by milliseconds with and without suffix
	cmd2, err := ParseArgs([]string{"wait", "1500"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	waitParams := cmd2.Params.(protocol.WaitParams)
	if waitParams.DurationMs != 1500 || waitParams.Selector != "" {
		t.Errorf("expected DurationMs 1500, got %+v", waitParams)
	}

	cmdMS, err := ParseArgs([]string{"wait", "1500ms"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmdMS.Params.(protocol.WaitParams).DurationMs != 1500 {
		t.Errorf("expected 1500ms -> 1500, got %d", cmdMS.Params.(protocol.WaitParams).DurationMs)
	}

	cmdSec, err := ParseArgs([]string{"wait", "2s"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmdSec.Params.(protocol.WaitParams).DurationMs != 2000 {
		t.Errorf("expected 2s -> 2000, got %d", cmdSec.Params.(protocol.WaitParams).DurationMs)
	}
	// wait by selector
	cmd3, err := ParseArgs([]string{"wait", ".spinner-done"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	waitParams3 := cmd3.Params.(protocol.WaitParams)
	if waitParams3.Selector != ".spinner-done" || waitParams3.State != "visible" {
		t.Errorf("expected selector .spinner-done visible, got %+v", waitParams3)
	}
}

func TestParseArgsScreenshotCloseStatusBroker(t *testing.T) {
	// screenshot without path
	cmd, err := ParseArgs([]string{"screenshot"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Method != protocol.MethodScreenshot || cmd.ScreenshotPath != "" {
		t.Errorf("unexpected screenshot cmd: %+v", cmd)
	}

	// screenshot with path
	cmd2, err := ParseArgs([]string{"screenshot", "output.png"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd2.ScreenshotPath != "output.png" {
		t.Errorf("expected ScreenshotPath 'output.png', got %q", cmd2.ScreenshotPath)
	}

	// close single
	cmd3, err := ParseArgs([]string{"close"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	closeParams := cmd3.Params.(protocol.CloseParams)
	if closeParams.CloseAll {
		t.Errorf("expected CloseAll false")
	}

	// close all
	cmd4, err := ParseArgs([]string{"close", "--all"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	closeParams4 := cmd4.Params.(protocol.CloseParams)
	if !closeParams4.CloseAll {
		t.Errorf("expected CloseAll true")
	}

	// status
	cmd5, err := ParseArgs([]string{"status"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd5.Method != protocol.MethodStatus {
		t.Errorf("expected Method %q, got %q", protocol.MethodStatus, cmd5.Method)
	}

	// broker
	cmd6, err := ParseArgs([]string{"broker", "start"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd6.Name != "broker" || cmd6.BrokerSubcmd != "start" {
		t.Errorf("unexpected broker command: %+v", cmd6)
	}
	// tabs
	cmdTabs, err := ParseArgs([]string{"tabs"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmdTabs.Method != protocol.MethodTabList {
		t.Errorf("expected Method %q, got %q", protocol.MethodTabList, cmdTabs.Method)
	}

	// switch
	cmdSwitch, err := ParseArgs([]string{"switch", "target-42"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmdSwitch.Method != protocol.MethodTabSwitch {
		t.Errorf("expected Method %q, got %q", protocol.MethodTabSwitch, cmdSwitch.Method)
	}
	if cmdSwitch.Params.(protocol.TabSwitchParams).TargetID != "target-42" {
		t.Errorf("expected targetId target-42, got %q", cmdSwitch.Params.(protocol.TabSwitchParams).TargetID)
	}
}

func TestParseArgsGlobalFlags(t *testing.T) {
	// Global flags before command
	cmd, err := ParseArgs([]string{"--json", "--timeout", "5000", "--session", "test-session", "open", "http://example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cmd.Global.JSON {
		t.Errorf("expected Global.JSON true")
	}

	if cmd.Global.TimeoutMs != 5000 {
		t.Errorf("expected Global.TimeoutMs 5000, got %d", cmd.Global.TimeoutMs)
	}
	if cmd.Global.Session != "test-session" {
		t.Errorf("expected Global.Session 'test-session', got %q", cmd.Global.Session)
	}
	if cmd.Global.Tab != "" {
		t.Errorf("expected empty Global.Tab")
	}

	cmdTab, err := ParseArgs([]string{"--tab", "tab-99", "snapshot"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmdTab.Global.Tab != "tab-99" {
		t.Errorf("expected Global.Tab 'tab-99', got %q", cmdTab.Global.Tab)
	}
	openParams := cmd.Params.(protocol.OpenParams)
	if openParams.TimeoutMs != 5000 {
		t.Errorf("expected OpenParams.TimeoutMs 5000, got %d", openParams.TimeoutMs)
	}

	// Global flags after command with = syntax
	cmd2, err := ParseArgs([]string{"snapshot", "-i", "--json", "--timeout=2500", "--session=sess2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cmd2.Global.JSON || cmd2.Global.TimeoutMs != 2500 || cmd2.Global.Session != "sess2" {
		t.Errorf("unexpected global flags: %+v", cmd2.Global)
	}
	snapParams := cmd2.Params.(protocol.SnapshotParams)
	if !snapParams.InteractiveOnly {
		t.Errorf("expected InteractiveOnly true")
	}
}

func TestParseArgsValidationErrors(t *testing.T) {
	errorCases := [][]string{
		{},
		{"unknown-cmd"},
		{"open"},
		{"click"},
		{"dblclick"},
		{"fill"},
		{"fill", "@e1"},
		{"type"},
		{"type", "@e1"},
		{"press"},
		{"hover"},
		{"focus"},
		{"eval"},
		{"wait"},
		{"snapshot", "-d"},
		{"snapshot", "-d", "notanumber"},
		{"snapshot", "-s"},
		{"--timeout"},
		{"--timeout", "notanumber", "status"},
		{"--session"},
	}

	for _, args := range errorCases {
		_, err := ParseArgs(args)
		if err == nil {
			t.Errorf("expected error for args %v, got nil", args)
		}
	}
}
func TestParseArgsEndOfOptionsDelimiter(t *testing.T) {
	// Arguments after "--" must be preserved as literal positional arguments
	cmd, err := ParseArgs([]string{"fill", "#input", "--", "--json"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Global.JSON {
		t.Errorf("expected global JSON to be false when --json is after --")
	}
	fillParams := cmd.Params.(protocol.FillParams)
	if fillParams.Text != "--json" {
		t.Errorf("expected text to be '--json', got %q", fillParams.Text)
	}
}

func TestBrokerSocketHardening(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test-secure-broker.sock")

	broker := NewBroker(sockPath, "127.0.0.1:9333")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = broker.Run(ctx)
	}()

	var info os.FileInfo
	var err error
	for i := 0; i < 50; i++ {
		info, err = os.Lstat(sockPath)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("broker socket not created: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("expected socket permissions 0600, got: %04o", perm)
	}

	broker.Close()
	cancel()
}
