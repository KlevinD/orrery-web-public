package discussionsession

func (s *Service) SetExecutionDeleter(deleter ExecutionDeleter) {
	if s != nil {
		s.executionDeleter = deleter
	}
}

func (s *Service) SetArtifactCleaner(cleaner ConversationArtifactCleaner) {
	if s != nil {
		s.artifactCleaner = cleaner
	}
}

func (s *Service) SetRuntimeRecordCleaner(cleaner DiscussionRuntimeRecordCleaner) {
	if s != nil {
		s.runtimeRecordCleaner = cleaner
	}
}
