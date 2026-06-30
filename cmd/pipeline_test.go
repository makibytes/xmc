package cmd

import (
	"reflect"
	"testing"
)

func TestSplitPipeline_SingleStage(t *testing.T) {
	stages := splitPipeline("receive my-queue")
	if len(stages) != 1 {
		t.Fatalf("expected 1 stage, got %d", len(stages))
	}
	if stages[0] != "receive my-queue" {
		t.Errorf("stage = %q, want %q", stages[0], "receive my-queue")
	}
}

func TestSplitPipeline_MultiStage(t *testing.T) {
	stages := splitPipeline("subscribe topic | send queue")
	if len(stages) != 2 {
		t.Fatalf("expected 2 stages, got %d", len(stages))
	}
	if stages[0] != "subscribe topic" {
		t.Errorf("stage[0] = %q, want %q", stages[0], "subscribe topic")
	}
	if stages[1] != "send queue" {
		t.Errorf("stage[1] = %q, want %q", stages[1], "send queue")
	}
}

func TestSplitPipeline_QuotedPipe(t *testing.T) {
	stages := splitPipeline(`grep "a|b" file`)
	if len(stages) != 1 {
		t.Fatalf("expected 1 stage (pipe inside quotes), got %d: %v", len(stages), stages)
	}
}

func TestSplitPipeline_SingleQuotedPipe(t *testing.T) {
	stages := splitPipeline(`grep 'a|b' file`)
	if len(stages) != 1 {
		t.Fatalf("expected 1 stage (pipe inside single quotes), got %d: %v", len(stages), stages)
	}
}

func TestSplitPipeline_ThreeStages(t *testing.T) {
	stages := splitPipeline("receive q | grep -i hugo | jq .")
	if len(stages) != 3 {
		t.Fatalf("expected 3 stages, got %d", len(stages))
	}
	if stages[0] != "receive q" {
		t.Errorf("stage[0] = %q", stages[0])
	}
	if stages[1] != "grep -i hugo" {
		t.Errorf("stage[1] = %q", stages[1])
	}
	if stages[2] != "jq ." {
		t.Errorf("stage[2] = %q", stages[2])
	}
}

func TestSplitPipeline_Empty(t *testing.T) {
	stages := splitPipeline("")
	if len(stages) != 0 {
		t.Fatalf("expected 0 stages, got %d", len(stages))
	}
}

func TestClassifyStage_Verb(t *testing.T) {
	tests := []string{"send queue msg", "receive q", "subscribe topic", "publish t msg", "peek q", "forward a b", "manage list"}
	for _, text := range tests {
		s := classifyStage(text)
		if !s.isVerb {
			t.Errorf("classifyStage(%q).isVerb = false, want true", text)
		}
	}
}

func TestClassifyStage_External(t *testing.T) {
	tests := []string{"grep -i hugo", "jq .", "xxd", "cat file.txt", "ls -la"}
	for _, text := range tests {
		s := classifyStage(text)
		if s.isVerb {
			t.Errorf("classifyStage(%q).isVerb = true, want false", text)
		}
	}
}

func TestClassifyStage_Aliases(t *testing.T) {
	s := classifyStage("get queue")
	if !s.isVerb || s.verb != "get" {
		t.Errorf("classifyStage(\"get queue\") = verb=%v, verb=%q; want true, \"get\"", s.isVerb, s.verb)
	}

	s = classifyStage("respond q msg")
	if !s.isVerb || s.verb != "respond" {
		t.Errorf("classifyStage(\"respond q msg\") = verb=%v, verb=%q; want true, \"respond\"", s.isVerb, s.verb)
	}
}

func TestCoalesceStages_AdjacentExternals(t *testing.T) {
	stages := []pipelineStage{
		{isVerb: true, verb: "receive", raw: "receive q"},
		{isVerb: false, raw: "grep -i hugo"},
		{isVerb: false, raw: "jq ."},
		{isVerb: false, raw: "xxd"},
	}

	blocks := coalesceStages(stages)
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
	if !blocks[0].isVerb {
		t.Error("block[0] should be a verb")
	}
	if blocks[1].isVerb {
		t.Error("block[1] should be external")
	}
	if len(blocks[1].stages) != 3 {
		t.Errorf("block[1] should have 3 coalesced stages, got %d", len(blocks[1].stages))
	}
}

func TestCoalesceStages_VerbVerbVerb(t *testing.T) {
	stages := []pipelineStage{
		{isVerb: true, verb: "subscribe", raw: "subscribe t"},
		{isVerb: true, verb: "send", raw: "send q"},
	}

	blocks := coalesceStages(stages)
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks (verb|verb stay separate), got %d", len(blocks))
	}
	if !blocks[0].isVerb || !blocks[1].isVerb {
		t.Error("both blocks should be verb blocks")
	}
}

func TestCoalesceStages_Mixed(t *testing.T) {
	// receive q | jq . | send out
	stages := []pipelineStage{
		{isVerb: true, verb: "receive", raw: "receive q"},
		{isVerb: false, raw: "jq ."},
		{isVerb: true, verb: "send", raw: "send out"},
	}

	blocks := coalesceStages(stages)
	if len(blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(blocks))
	}
	if !blocks[0].isVerb {
		t.Error("block[0] should be verb")
	}
	if blocks[1].isVerb {
		t.Error("block[1] should be external")
	}
	if !blocks[2].isVerb {
		t.Error("block[2] should be verb")
	}
}

func TestShellSplit_Basic(t *testing.T) {
	words := shellSplit("send my-queue hello")
	if len(words) != 3 {
		t.Fatalf("expected 3 words, got %d: %v", len(words), words)
	}
	if words[0] != "send" || words[1] != "my-queue" || words[2] != "hello" {
		t.Errorf("words = %v", words)
	}
}

func TestShellSplit_Quoted(t *testing.T) {
	words := shellSplit(`send queue "hello world"`)
	if len(words) != 3 {
		t.Fatalf("expected 3 words, got %d: %v", len(words), words)
	}
	if words[2] != "hello world" {
		t.Errorf("words[2] = %q, want %q", words[2], "hello world")
	}
}

func TestShellSplit_SingleQuoted(t *testing.T) {
	words := shellSplit("send queue 'hello world'")
	if len(words) != 3 {
		t.Fatalf("expected 3 words, got %d: %v", len(words), words)
	}
	if words[2] != "hello world" {
		t.Errorf("words[2] = %q, want %q", words[2], "hello world")
	}
}

func TestShellSplit_Flags(t *testing.T) {
	words := shellSplit("receive q -n 5 -J")
	if len(words) != 5 {
		t.Fatalf("expected 5 words, got %d: %v", len(words), words)
	}
	if words[3] != "5" || words[4] != "-J" {
		t.Errorf("words = %v", words)
	}
}

func TestEnsureFlag_Adds(t *testing.T) {
	args := []string{"subscribe", "topic"}
	result := ensureFlag(args, "--ndjson")
	if len(result) != 3 || result[2] != "--ndjson" {
		t.Errorf("ensureFlag should add flag, got %v", result)
	}
}

func TestEnsureFlag_NoDoubles(t *testing.T) {
	args := []string{"subscribe", "topic", "--ndjson"}
	result := ensureFlag(args, "--ndjson")
	if len(result) != 3 {
		t.Errorf("ensureFlag should not duplicate, got %v", result)
	}
}

func TestIsProducer(t *testing.T) {
	if !isProducer("send") {
		t.Error("send should be a producer")
	}
	if !isProducer("publish") {
		t.Error("publish should be a producer")
	}
	if isProducer("receive") {
		t.Error("receive should not be a producer")
	}
}

func TestSplitCommands_Single(t *testing.T) {
	cmds := splitCommands("receive q -n 5")
	if len(cmds) != 1 || cmds[0] != "receive q -n 5" {
		t.Errorf("got %v", cmds)
	}
}

func TestSplitCommands_Multiple(t *testing.T) {
	cmds := splitCommands("manage create-queue a ; send a hello")
	if len(cmds) != 2 {
		t.Fatalf("expected 2, got %d", len(cmds))
	}
	if cmds[0] != "manage create-queue a" {
		t.Errorf("cmds[0] = %q", cmds[0])
	}
	if cmds[1] != "send a hello" {
		t.Errorf("cmds[1] = %q", cmds[1])
	}
}

func TestSplitCommands_TrailingSemicolon(t *testing.T) {
	cmds := splitCommands("send q hi ;")
	if len(cmds) != 1 || cmds[0] != "send q hi" {
		t.Errorf("got %v", cmds)
	}
}

func TestSplitCommands_EmptySegments(t *testing.T) {
	cmds := splitCommands("; ; send q hi ; ;")
	if len(cmds) != 1 || cmds[0] != "send q hi" {
		t.Errorf("got %v", cmds)
	}
}

func TestSplitCommands_QuotedSemicolon(t *testing.T) {
	cmds := splitCommands(`send q "hello ; world"`)
	if len(cmds) != 1 {
		t.Fatalf("semicolon inside quotes should not split, got %d", len(cmds))
	}
}

func TestSplitCommands_SingleQuotedSemicolon(t *testing.T) {
	cmds := splitCommands(`send q 'a ; b'`)
	if len(cmds) != 1 {
		t.Fatalf("semicolon inside single quotes should not split, got %d", len(cmds))
	}
}

func TestSplitCommands_WithPipeline(t *testing.T) {
	cmds := splitCommands("receive q -n 5 | jq . ; send q2 hello")
	if len(cmds) != 2 {
		t.Fatalf("expected 2, got %d", len(cmds))
	}
	if cmds[0] != "receive q -n 5 | jq ." {
		t.Errorf("cmds[0] = %q", cmds[0])
	}
}

func TestSplitOnDelim_BackslashEscape(t *testing.T) {
	tests := []struct {
		name  string
		input string
		delim rune
		want  []string
	}{
		{name: "escaped pipe", input: "a \\| b", delim: '|', want: []string{"a \\| b"}},
		{name: "trailing backslash", input: "a \\", delim: '|', want: []string{"a \\"}},
		{name: "double backslash then pipe", input: "a \\\\| b", delim: '|', want: []string{"a \\\\", "b"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := splitOnDelim(tc.input, tc.delim)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("splitOnDelim(%q, %c) = %v, want %v", tc.input, tc.delim, got, tc.want)
			}
		})
	}
}

func TestExpandAlias_NoAliases(t *testing.T) {
	got := expandAlias("send q hello", nil)
	if got != "send q hello" {
		t.Errorf("without aliases: got %q, want %q", got, "send q hello")
	}
}

func TestExpandAlias_NoMatch(t *testing.T) {
	aliases := map[string]string{"hi": "send $1"}
	got := expandAlias("send q hello", aliases)
	if got != "send q hello" {
		t.Errorf("no match: got %q, want %q", got, "send q hello")
	}
}

func TestExpandAlias_SimpleSubstitution(t *testing.T) {
	aliases := map[string]string{"hi": "send $1"}
	got := expandAlias("hi my-queue", aliases)
	if got != "send my-queue" {
		t.Errorf("simple substitution: got %q, want %q", got, "send my-queue")
	}
}

func TestExpandAlias_MultipleArgs(t *testing.T) {
	aliases := map[string]string{"hi2": "send $2 $1"}
	got := expandAlias("hi2 hello world", aliases)
	if got != "send world hello" {
		t.Errorf("multiple args: got %q, want %q", got, "send world hello")
	}
}

func TestExpandAlias_AtStarAll(t *testing.T) {
	aliases := map[string]string{"hiAll": "send $@"}
	got := expandAlias("hiAll a b c", aliases)
	if got != "send a b c" {
		t.Errorf("$@: got %q, want %q", got, "send a b c")
	}
}

func TestExpandAlias_AtStarAllAsterisk(t *testing.T) {
	aliases := map[string]string{"hiAll": "send $*"}
	got := expandAlias("hiAll a b c", aliases)
	if got != "send a b c" {
		t.Errorf("$*: got %q, want %q", got, "send a b c")
	}
}

func TestExpandAlias_MixedLiteralAndSubstitution(t *testing.T) {
	aliases := map[string]string{"peek1": "peek $1 -n 1"}
	got := expandAlias("peek1 my-queue", aliases)
	want := "peek my-queue -n 1"
	if got != want {
		t.Errorf("mixed: got %q, want %q", got, want)
	}
}

func TestExpandAlias_DollarWithoutNumber(t *testing.T) {
	aliases := map[string]string{"sendRaw": "send -P format=raw $@"}
	got := expandAlias("sendRaw my-queue hello", aliases)
	if got != "send -P format=raw my-queue hello" {
		t.Errorf("dollar without number: got %q", got)
	}
}

func TestExpandAlias_EmptyArgs(t *testing.T) {
	aliases := map[string]string{"hi": "send $1 $2"}
	got := expandAlias("hi", aliases)
	want := "send  "
	if got != want {
		t.Errorf("empty args: got %q, want %q", got, want)
	}
}

func TestExpandAlias_EmptyLine(t *testing.T) {
	aliases := map[string]string{"hi": "send $1"}
	got := expandAlias("", aliases)
	if got != "" {
		t.Errorf("empty line: got %q, want %q", got, "")
	}
}

func TestPrevBlockIsVerb(t *testing.T) {
	blocks := []pipelineBlock{
		{isVerb: false},
		{isVerb: true},
		{isVerb: false},
	}
	if prevBlockIsVerb(blocks, 1) {
		t.Error("prevBlockIsVerb(blocks, 1) should be false (prev is external)")
	}
	if !prevBlockIsVerb(blocks, 2) {
		t.Error("prevBlockIsVerb(blocks, 2) should be true (prev is verb)")
	}
	if prevBlockIsVerb(blocks, 0) {
		t.Error("prevBlockIsVerb(blocks, 0) should be false (no prev)")
	}
}

func TestExpandAlias_NoArgsButTemplateUsesAt(t *testing.T) {
	aliases := map[string]string{"cmd": "build $@"}
	got := expandAlias("cmd", aliases)
	want := "build "
	if got != want {
		t.Errorf("$@ no args: got %q, want %q", got, want)
	}
}

func TestIsConsumer(t *testing.T) {
	if !isConsumer("receive") {
		t.Error("receive should be a consumer")
	}
	if !isConsumer("subscribe") {
		t.Error("subscribe should be a consumer")
	}
	if isConsumer("send") {
		t.Error("send should not be a consumer")
	}
}
