package cmd

import (
	"testing"
)

func TestContainsVerb_WithVerb(t *testing.T) {
	if !containsVerb("receive my-queue") {
		t.Error("expected true for 'receive my-queue'")
	}
	if !containsVerb("subscribe topic | send queue") {
		t.Error("expected true for pipeline with verbs")
	}
	if !containsVerb("grep foo | send queue") {
		t.Error("expected true for mixed pipeline with verb")
	}
}

func TestContainsVerb_WithoutVerb(t *testing.T) {
	if containsVerb("ls -la") {
		t.Error("expected false for 'ls -la'")
	}
	if containsVerb("grep -i hugo | jq .") {
		t.Error("expected false for all-external pipeline")
	}
	if containsVerb("cat file.txt") {
		t.Error("expected false for 'cat file.txt'")
	}
}
