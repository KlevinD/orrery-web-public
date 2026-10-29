package discussionsession

import "testing"

func TestPersonalAssistantRuntimeMarkerRequiresCompleteOrigin(t *testing.T) {
	for name, record := range map[string]ConversationRecord{
		"owner only": {
			PersonalAssistantOriginOwnerID: "account-1",
		},
		"subject only": {
			PersonalAssistantOriginSubjectID: "user-1",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if isPersonalAssistantRuntimeConversation(record) {
				t.Fatalf("incomplete PA origin must not enable PA runtime: %+v", record)
			}
		})
	}

	if !isPersonalAssistantRuntimeConversation(ConversationRecord{
		PersonalAssistantOriginOwnerID:   "account-1",
		PersonalAssistantOriginSubjectID: "user-1",
	}) {
		t.Fatal("complete PA origin must enable PA runtime")
	}
}
