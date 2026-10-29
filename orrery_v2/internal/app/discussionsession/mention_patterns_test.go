package discussionsession

import (
	"reflect"
	"testing"
)

func TestParseDiscussionMentionLinksRejectsMalformedTargets(t *testing.T) {
	content := "[@Lead](mention://agent/lead-agent) [@User](mention://user/user-1) " +
		"[@Bad](mention://agent/id with spaces) [@NoKind](mention:///missing)"
	links := parseDiscussionMentionLinks(content)
	if len(links) != 2 {
		t.Fatalf("links = %+v, want two valid links", links)
	}
	if links[0].Kind != "agent" || links[0].ID != "lead-agent" || links[0].Label != "Lead" {
		t.Fatalf("agent link = %+v", links[0])
	}
	if links[1].Kind != "user" || links[1].ID != "user-1" || links[1].Label != "User" {
		t.Fatalf("user link = %+v", links[1])
	}
}

func TestDiscussionAgentMentionIDsPreservesOrder(t *testing.T) {
	got := discussionAgentMentionIDs("[broken [@A](mention://agent/a) then [@B](mention://agent/b) and [@U](mention://user/u)")
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("ids = %v", got)
	}
}

func TestDiscussionAgentMentionIDsAcceptsBareProviderLinks(t *testing.T) {
	content := "已安排执行。\n\nmention://agent/profile-f54c4401bfb058eadd0e48a5a878b4e4 请负责交付。"
	got := discussionAgentMentionIDs(content)
	if !reflect.DeepEqual(got, []string{"profile-f54c4401bfb058eadd0e48a5a878b4e4"}) {
		t.Fatalf("ids = %v", got)
	}
}

func TestParseDiscussionMentionLinksDoesNotDowngradeMalformedMarkdownToBareLink(t *testing.T) {
	links := parseDiscussionMentionLinks("[@Bad](mention://agent/id with spaces)")
	if len(links) != 0 {
		t.Fatalf("links = %+v, want malformed markdown mention rejected", links)
	}
}

func TestHasPlainDiscussionMentionUsesTokenBoundaries(t *testing.T) {
	for _, content := range []string{"@Lead please", "hello，@秦若兰", "hello @agent_1"} {
		if !hasPlainDiscussionMention(content) {
			t.Fatalf("expected plain mention in %q", content)
		}
	}
	for _, content := range []string{"email@example.com", "prefix@Lead", "@ mention"} {
		if hasPlainDiscussionMention(content) {
			t.Fatalf("unexpected plain mention in %q", content)
		}
	}
}
