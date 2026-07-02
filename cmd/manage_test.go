package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// runManage executes one manage subcommand against spec and returns its output.
func runManage(t *testing.T, spec ManageSpec, args ...string) string {
	t.Helper()
	cmd := NewManageCommand(spec)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("manage %v: %v", args, err)
	}
	return out.String()
}

func TestManageCommand_QueueLifecycleActions(t *testing.T) {
	var got []string
	record := func(op string) *ManageAction {
		return &ManageAction{Run: func(name string) error {
			got = append(got, op+":"+name)
			return nil
		}}
	}
	spec := ManageSpec{
		UpdateQueue:  record("update"),
		EnableQueue:  record("enable"),
		DisableQueue: record("disable"),
	}

	out := runManage(t, spec, "update-queue", "q1")
	if !strings.Contains(out, "Updated queue q1") {
		t.Errorf("update output = %q", out)
	}
	out = runManage(t, spec, "enable-queue", "q2")
	if !strings.Contains(out, "Enabled queue q2") {
		t.Errorf("enable output = %q", out)
	}
	out = runManage(t, spec, "disable-queue", "q3")
	if !strings.Contains(out, "Disabled queue q3") {
		t.Errorf("disable output = %q", out)
	}
	want := []string{"update:q1", "enable:q2", "disable:q3"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("recorded calls = %v, want %v", got, want)
	}
}

func TestManageCommand_ActionsOmittedWhenNil(t *testing.T) {
	cmd := NewManageCommand(ManageSpec{})
	for _, name := range []string{"update-queue", "enable-queue", "disable-queue", "bind-queue", "unbind-queue"} {
		for _, sub := range cmd.Commands() {
			if strings.HasPrefix(sub.Use, name+" ") || sub.Name() == name {
				t.Errorf("subcommand %q registered without a spec action", name)
			}
		}
	}
}

func TestManageCommand_BindActionDefaultNoun(t *testing.T) {
	var boundQueue, boundTarget string
	spec := ManageSpec{
		BindQueue: &BindAction{Run: func(queue, target string) error {
			boundQueue, boundTarget = queue, target
			return nil
		}},
	}
	cmd := NewManageCommand(spec)
	bind := findSubcommand(t, cmd, "bind-queue")
	if bind.Use != "bind-queue <queue> <exchange>" {
		t.Errorf("Use = %q", bind.Use)
	}
	if bind.Short != "Bind a queue to an exchange" {
		t.Errorf("Short = %q", bind.Short)
	}

	out := runManage(t, spec, "bind-queue", "q", "ex")
	if boundQueue != "q" || boundTarget != "ex" {
		t.Errorf("Run got (%q, %q)", boundQueue, boundTarget)
	}
	if !strings.Contains(out, "Bound queue q to exchange ex") {
		t.Errorf("success output = %q", out)
	}
}

func TestManageCommand_BindActionTargetNoun(t *testing.T) {
	spec := ManageSpec{
		BindQueue: &BindAction{
			TargetNoun: "address",
			Run:        func(queue, target string) error { return nil },
		},
	}
	cmd := NewManageCommand(spec)
	bind := findSubcommand(t, cmd, "bind-queue")
	if bind.Use != "bind-queue <queue> <address>" {
		t.Errorf("Use = %q", bind.Use)
	}
	if bind.Short != "Bind a queue to an address" {
		t.Errorf("Short = %q", bind.Short)
	}

	out := runManage(t, spec, "bind-queue", "q", "addr")
	if !strings.Contains(out, "Bound queue q to address addr") {
		t.Errorf("success output = %q", out)
	}
}

func findSubcommand(t *testing.T, parent *cobra.Command, name string) *cobra.Command {
	t.Helper()
	for _, sub := range parent.Commands() {
		if sub.Name() == name {
			return sub
		}
	}
	t.Fatalf("subcommand %q not found", name)
	return nil
}
