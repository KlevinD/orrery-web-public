package operations

func (s *QueryService) SetReBACStore(store ReBACStorePort) {
	if s != nil {
		s.rebac = store
	}
}
