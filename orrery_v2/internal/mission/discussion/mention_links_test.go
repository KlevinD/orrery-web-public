package discussion

import (
	"reflect"
	"strings"
	"testing"
)

func TestOwnedMentionLinksPreserveProductTargets(t *testing.T) {
	input := "mention://agent/bare\n[@甲[检查]](mention://agent/agent-42) [planner](mention://role/Planner) [人](mention://user/user-42) [团队](mention://team/team-42) [全员](mention://all/all) [again](mention://agent/agent-42)"
	expected := []MentionTarget{{Kind: MentionTargetAgent, Ref: "agent-42"}, {Kind: MentionTargetRole, Ref: "Planner", Role: "Planner"}, {Kind: MentionTargetUser, Ref: "user-42"}, {Kind: MentionTargetTeam, Ref: "team-42"}, {Kind: MentionTargetAll, Ref: "all"}, {Kind: MentionTargetAgent, Ref: "bare"}}
	var parser MentionParser
	got := parser.Parse(input)
	if !reflect.DeepEqual(got.Targets, expected) || len(got.Warnings) != 0 {
		t.Fatalf("targets: %+v", got)
	}
}

func TestOwnedMentionLinksBoundaryAndMalformedInputs(t *testing.T) {
	for _, sample := range []struct {
		input string
		ref   string
	}{
		{"[@QA]review](mention://agent/qa)", "qa"},
		{"[@[QA]](mention://agent/qa)", "qa"},
		{"请处理，mention://agent/qa", "qa"},
		{"\tmention://agent/qa-42_id", "qa-42_id"},
		{"[] (mention://agent/qa)", ""},
		{"[](mention://agent/qa)", ""},
		{"[line\nbreak](mention://agent/qa)", ""},
		{"[line\rbreak](mention://agent/qa)", "qa"},
		{"[missing](mention://agent/)", ""},
		{"[missing](mention://agent/qa", ""},
		{"embeddedmention://agent/qa", ""},
		{"https://example.test/mention://agent/qa", ""},
	} {
		t.Run(sample.input, func(t *testing.T) {
			got := NewMentionParser().Parse(sample.input)
			if sample.ref == "" {
				if len(got.Targets) != 0 {
					t.Fatalf("unexpected target: %+v", got)
				}
				return
			}
			if len(got.Targets) != 1 || got.Targets[0].Ref != sample.ref {
				t.Fatalf("targets: %+v", got)
			}
		})
	}
}

func TestOwnedMentionLinksWarningsAndDeduplication(t *testing.T) {
	got := NewMentionParser().Parse("[file](mention://document/doc-42) [x](mention://agent/a) mention://agent/a mention://user/a")
	if len(got.Warnings) != 1 || got.Warnings[0].Target.Kind != "document" || got.Warnings[0].Target.Ref != "doc-42" {
		t.Fatalf("warnings: %+v", got.Warnings)
	}
	if len(got.Targets) != 2 || got.Targets[0].AgentID() != "a" || got.Targets[1].AgentID() != "" {
		t.Fatalf("deduplication: %+v", got.Targets)
	}
	if targets := NewMentionParser().Parse(strings.Repeat("[", 100000) + "x](mention://agent/a)").Targets; len(targets) != 1 {
		t.Fatalf("long label: %+v", targets)
	}
	if targets := NewMentionParser().Parse("[" + strings.Repeat("unfinished](mention://agent/a", 10000)).Targets; len(targets) != 0 {
		t.Fatalf("unfinished links: %+v", targets)
	}
}
