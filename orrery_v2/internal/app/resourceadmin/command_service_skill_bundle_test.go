package resourceadmin

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func TestCommandServiceSerializesCascadeDeletionWithConcurrentAgentBinding(t *testing.T) {
	ctx := context.Background()
	skills := skillres.NewMemoryRegistry()
	registerBundleTestSkill(t, skills, "skill.review", "Review")
	bundleStore := skillres.NewMemoryBundleRegistry()
	bundles := &blockingBundleRegistry{
		BundleRegistry: bundleStore,
		listStarted:    make(chan struct{}),
		releaseList:    make(chan struct{}),
	}
	profiles := newConcurrentAgentProfileStore(agentcore.AgentProfile{
		ID:        "profile-reviewer",
		RealName:  "Reviewer",
		AgentKind: agentcore.AgentKindAI,
		Enabled:   true,
	})
	command := NewCommandService(toolres.NewMemoryRegistry(), skills)
	command.SetSkillBundleRegistry(bundles)
	command.SetAgentProfileBindingStore(profiles)
	command.SetIDGenerator(&sequenceIDGenerator{values: []string{"skill-bundle-review"}})
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}
	created, err := command.CreateSkillBundle(ctx, viewer, CreateSkillBundleRequest{
		Name:     "Review",
		SkillIDs: []string{"skill.review"},
	})
	if err != nil {
		t.Fatalf("CreateSkillBundle: %v", err)
	}

	deleteResult := make(chan error, 1)
	go func() {
		deleteResult <- command.DeleteSkillBundle(ctx, viewer, created.BundleID, DeleteSkillBundleRequest{DeleteSkills: true})
	}()
	<-bundles.listStarted

	bindResult := make(chan error, 1)
	go func() {
		_, err := command.BindResourceToAgent(ctx, viewer, BindResourceRequest{
			AgentID:      "profile-reviewer",
			ResourceID:   "skill.review",
			ResourceType: "skill",
		})
		bindResult <- err
	}()

	var earlyBindErr error
	bindCompletedEarly := false
	select {
	case earlyBindErr = <-bindResult:
		bindCompletedEarly = true
	case <-time.After(200 * time.Millisecond):
	}
	close(bundles.releaseList)
	deleteErr := <-deleteResult
	if bindCompletedEarly {
		t.Fatalf("binding completed during cascade preflight with error %v; deletion returned %v", earlyBindErr, deleteErr)
	}
	if deleteErr != nil {
		t.Fatalf("DeleteSkillBundle: %v", deleteErr)
	}
	if err := <-bindResult; !errors.Is(err, skillres.ErrNotFound) {
		t.Fatalf("concurrent binding error = %v, want skill not found after deletion", err)
	}
	if refs := profiles.skillRefs(); len(refs) != 0 {
		t.Fatalf("concurrent binding persisted stale refs: %+v", refs)
	}
}

type blockingBundleRegistry struct {
	skillres.BundleRegistry
	listStarted chan struct{}
	releaseList chan struct{}
	startOnce   sync.Once
}

func (r *blockingBundleRegistry) List(ctx context.Context) ([]skillres.SkillBundle, error) {
	r.startOnce.Do(func() { close(r.listStarted) })
	select {
	case <-r.releaseList:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return r.BundleRegistry.List(ctx)
}

type concurrentAgentProfileStore struct {
	mu      sync.Mutex
	profile agentcore.AgentProfile
}

func newConcurrentAgentProfileStore(profile agentcore.AgentProfile) *concurrentAgentProfileStore {
	return &concurrentAgentProfileStore{profile: cloneBundleTestProfile(profile)}
}

func (s *concurrentAgentProfileStore) GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	if err := ctx.Err(); err != nil {
		return agentcore.AgentProfile{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.profile.ID != id {
		return agentcore.AgentProfile{}, errors.New("profile not found")
	}
	return cloneBundleTestProfile(s.profile), nil
}

func (s *concurrentAgentProfileStore) SaveProfile(ctx context.Context, profile agentcore.AgentProfile) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profile = cloneBundleTestProfile(profile)
	return nil
}

func (s *concurrentAgentProfileStore) QueryDirectory(ctx context.Context, _ agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	if err := ctx.Err(); err != nil {
		return agentcore.AgentDirectoryPage{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
		AgentProfileID: s.profile.ID,
		DisplayName:    s.profile.RealName,
		SkillRefs:      append([]agentcore.SkillRef(nil), s.profile.SkillRefs...),
	}}}, nil
}

func (s *concurrentAgentProfileStore) skillRefs() []agentcore.SkillRef {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]agentcore.SkillRef(nil), s.profile.SkillRefs...)
}

func cloneBundleTestProfile(profile agentcore.AgentProfile) agentcore.AgentProfile {
	profile.SkillRefs = append([]agentcore.SkillRef(nil), profile.SkillRefs...)
	profile.ToolRefs = append([]agentcore.ToolRef(nil), profile.ToolRefs...)
	return profile
}

type failingSkillDeleteRegistry struct {
	skillres.Registry
	mu      sync.Mutex
	failID  agentcore.SkillID
	failErr error
	failed  bool
}

type blockingSkillRegisterRegistry struct {
	skillres.Registry
	registerStarted chan struct{}
	releaseRegister chan struct{}
	startOnce       sync.Once
}

func (r *blockingSkillRegisterRegistry) Register(ctx context.Context, definition skillres.SkillDefinition) error {
	r.startOnce.Do(func() { close(r.registerStarted) })
	select {
	case <-r.releaseRegister:
	case <-ctx.Done():
		return ctx.Err()
	}
	return r.Registry.Register(ctx, definition)
}

type failingBundleDeleteRegistry struct {
	skillres.BundleRegistry
	failErr     error
	deleteFirst bool
}

type failingActionAuditRecorder struct {
	failAction string
	failErr    error
	events     []AuditEvent
}

type cancellingAuditRecorder struct {
	cancel func()
}

func (r cancellingAuditRecorder) RecordAuditEvent(context.Context, AuditEvent) error {
	r.cancel()
	return context.Canceled
}

func (r *failingActionAuditRecorder) RecordAuditEvent(_ context.Context, event AuditEvent) error {
	if event.Action == r.failAction {
		return r.failErr
	}
	r.events = append(r.events, event)
	return nil
}

func (r *failingBundleDeleteRegistry) Delete(ctx context.Context, id string) error {
	if r.deleteFirst {
		if err := r.BundleRegistry.Delete(ctx, id); err != nil {
			return err
		}
	}
	return r.failErr
}

func (r *failingSkillDeleteRegistry) Delete(ctx context.Context, id agentcore.SkillID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id == r.failID && !r.failed {
		r.failed = true
		return r.failErr
	}
	return r.Registry.Delete(ctx, id)
}

func TestCommandServiceCreatesAndManagesSkillBundleMembership(t *testing.T) {
	ctx := context.Background()
	skills := skillres.NewMemoryRegistry()
	registerBundleTestSkill(t, skills, "skill.review", "Review")
	registerBundleTestSkill(t, skills, "skill.test", "Test")
	bundles := skillres.NewMemoryBundleRegistry()
	command := NewCommandService(toolres.NewMemoryRegistry(), skills)
	command.SetSkillBundleRegistry(bundles)
	command.SetIDGenerator(&sequenceIDGenerator{values: []string{"skill-bundle-000001"}})
	query := NewQueryService(toolres.NewMemoryRegistry(), skills)
	query.SetSkillBundleRegistry(bundles)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage, PermissionResourceRead}}

	created, err := command.CreateSkillBundle(ctx, viewer, CreateSkillBundleRequest{
		Name:        "Quality pack",
		Description: "Review and test",
		SkillIDs:    []string{"skill.review", "skill.review"},
	})
	if err != nil {
		t.Fatalf("CreateSkillBundle: %v", err)
	}
	if created.BundleID != "skill-bundle-000001" || len(created.SkillIDs) != 1 || len(created.Skills) != 1 {
		t.Fatalf("created = %+v", created)
	}

	updated, err := command.AddSkillsToBundle(ctx, viewer, created.BundleID, AddSkillsToBundleRequest{
		SkillIDs: []string{"skill.review", "skill.test"},
	})
	if err != nil {
		t.Fatalf("AddSkillsToBundle: %v", err)
	}
	if len(updated.SkillIDs) != 2 || updated.SkillIDs[0] != "skill.review" || updated.SkillIDs[1] != "skill.test" {
		t.Fatalf("updated skill ids = %+v", updated.SkillIDs)
	}

	page, err := query.ListSkillBundles(ctx, viewer)
	if err != nil {
		t.Fatalf("ListSkillBundles: %v", err)
	}
	if len(page.Bundles) != 1 || len(page.Bundles[0].Skills) != 2 || page.Bundles[0].Skills[1].Name != "Test" {
		t.Fatalf("bundle page = %+v", page)
	}

	removed, err := command.RemoveSkillFromBundle(ctx, viewer, created.BundleID, "skill.review")
	if err != nil {
		t.Fatalf("RemoveSkillFromBundle: %v", err)
	}
	if len(removed.SkillIDs) != 1 || removed.SkillIDs[0] != "skill.test" {
		t.Fatalf("removed = %+v", removed)
	}

	if _, err := command.AddSkillsToBundle(ctx, viewer, created.BundleID, AddSkillsToBundleRequest{SkillIDs: []string{"skill.missing"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing skill error = %v, want ErrNotFound", err)
	}
}

func TestCommandServiceImportsSkillIntoBundle(t *testing.T) {
	ctx := context.Background()
	skills := skillres.NewMemoryRegistry()
	bundles := skillres.NewMemoryBundleRegistry()
	store := skillres.NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	command := NewCommandService(toolres.NewMemoryRegistry(), skills)
	command.SetSkillBundleRegistry(bundles)
	command.SetSkillPackageStore(store)
	command.SetIDGenerator(&sequenceIDGenerator{values: []string{"skill-bundle-import"}})
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}

	created, err := command.CreateSkillBundle(ctx, viewer, CreateSkillBundleRequest{Name: "Imported pack"})
	if err != nil {
		t.Fatalf("CreateSkillBundle: %v", err)
	}
	pkg := buildSkillPackage(t, "skill.imported", "Imported", "# Imported\n")
	updated, err := command.ImportSkillIntoBundle(ctx, viewer, created.BundleID, bytes.NewReader(pkg), int64(len(pkg)))
	if err != nil {
		t.Fatalf("ImportSkillIntoBundle: %v", err)
	}
	if len(updated.SkillIDs) != 1 || updated.SkillIDs[0] != "skill.imported" {
		t.Fatalf("updated = %+v", updated)
	}
	if _, err := skills.Get(ctx, "skill.imported"); err != nil {
		t.Fatalf("imported skill missing from registry: %v", err)
	}
}

func TestCommandServiceSerializesBundleImportWithConcurrentDeletion(t *testing.T) {
	ctx := context.Background()
	baseSkills := skillres.NewMemoryRegistry()
	skills := &blockingSkillRegisterRegistry{
		Registry:        baseSkills,
		registerStarted: make(chan struct{}),
		releaseRegister: make(chan struct{}),
	}
	bundles := skillres.NewMemoryBundleRegistry()
	store := skillres.NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	command := NewCommandService(toolres.NewMemoryRegistry(), skills)
	command.SetSkillBundleRegistry(bundles)
	command.SetSkillPackageStore(store)
	command.SetIDGenerator(&sequenceIDGenerator{values: []string{"skill-bundle-import"}})
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}
	created, err := command.CreateSkillBundle(ctx, viewer, CreateSkillBundleRequest{Name: "Imported pack"})
	if err != nil {
		t.Fatalf("CreateSkillBundle: %v", err)
	}
	pkg := buildSkillPackage(t, "skill.imported", "Imported", "# Imported\n")
	importResult := make(chan error, 1)
	go func() {
		_, err := command.ImportSkillIntoBundle(ctx, viewer, created.BundleID, bytes.NewReader(pkg), int64(len(pkg)))
		importResult <- err
	}()
	<-skills.registerStarted

	deleteResult := make(chan error, 1)
	go func() {
		deleteResult <- command.DeleteSkillBundle(ctx, viewer, created.BundleID, DeleteSkillBundleRequest{})
	}()
	var earlyDeleteErr error
	deleteCompletedEarly := false
	select {
	case earlyDeleteErr = <-deleteResult:
		deleteCompletedEarly = true
	case <-time.After(200 * time.Millisecond):
	}
	close(skills.releaseRegister)
	importErr := <-importResult
	if deleteCompletedEarly {
		t.Fatalf("bundle deletion completed during import with error %v; import returned %v", earlyDeleteErr, importErr)
	}
	if importErr != nil {
		t.Fatalf("ImportSkillIntoBundle: %v", importErr)
	}
	if err := <-deleteResult; err != nil {
		t.Fatalf("DeleteSkillBundle: %v", err)
	}
}

func TestCommandServiceChecksBundleImportPermissionBeforeBundleLookup(t *testing.T) {
	command := NewCommandService(toolres.NewMemoryRegistry(), skillres.NewMemoryRegistry())
	command.SetSkillBundleRegistry(skillres.NewMemoryBundleRegistry())

	_, err := command.ImportSkillIntoBundle(
		context.Background(),
		ViewerRef{ViewerID: "reader", PermissionScope: []string{PermissionResourceRead}},
		"missing-bundle",
		bytes.NewReader(nil),
		0,
	)
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("ImportSkillIntoBundle error = %v, want ErrPermissionDenied", err)
	}
}

func TestCommandServiceDeletesBundleWithoutDeletingSkillsByDefault(t *testing.T) {
	ctx := context.Background()
	skills := skillres.NewMemoryRegistry()
	registerBundleTestSkill(t, skills, "skill.review", "Review")
	bundles := skillres.NewMemoryBundleRegistry()
	command := NewCommandService(toolres.NewMemoryRegistry(), skills)
	command.SetSkillBundleRegistry(bundles)
	command.SetIDGenerator(&sequenceIDGenerator{values: []string{"skill-bundle-review"}})
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}
	created, err := command.CreateSkillBundle(ctx, viewer, CreateSkillBundleRequest{Name: "Review pack", SkillIDs: []string{"skill.review"}})
	if err != nil {
		t.Fatalf("CreateSkillBundle: %v", err)
	}

	if err := command.DeleteSkillBundle(ctx, viewer, created.BundleID, DeleteSkillBundleRequest{}); err != nil {
		t.Fatalf("DeleteSkillBundle: %v", err)
	}
	if _, err := bundles.Get(ctx, created.BundleID); !errors.Is(err, skillres.ErrNotFound) {
		t.Fatalf("bundle get error = %v, want ErrNotFound", err)
	}
	if _, err := skills.Get(ctx, "skill.review"); err != nil {
		t.Fatalf("non-cascade delete removed skill: %v", err)
	}
}

func TestCommandServiceCascadeDeletesUnboundBundleSkills(t *testing.T) {
	ctx := context.Background()
	skills := skillres.NewMemoryRegistry()
	bundles := skillres.NewMemoryBundleRegistry()
	store := skillres.NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	profiles := &fakeAgentProfileBindingStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	command := NewCommandService(toolres.NewMemoryRegistry(), skills)
	command.SetSkillBundleRegistry(bundles)
	command.SetSkillPackageStore(store)
	command.SetAgentProfileBindingStore(profiles)
	command.SetIDGenerator(&sequenceIDGenerator{values: []string{"skill-bundle-quality"}})
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}

	for _, id := range []string{"skill.review", "skill.test"} {
		pkg := buildSkillPackage(t, id, id, "# Skill\n")
		if _, err := command.ImportSkillPackage(ctx, viewer, bytes.NewReader(pkg), int64(len(pkg))); err != nil {
			t.Fatalf("ImportSkillPackage(%s): %v", id, err)
		}
	}
	created, err := command.CreateSkillBundle(ctx, viewer, CreateSkillBundleRequest{Name: "Quality", SkillIDs: []string{"skill.review", "skill.test"}})
	if err != nil {
		t.Fatalf("CreateSkillBundle: %v", err)
	}

	if err := command.DeleteSkillBundle(ctx, viewer, created.BundleID, DeleteSkillBundleRequest{DeleteSkills: true}); err != nil {
		t.Fatalf("DeleteSkillBundle cascade: %v", err)
	}
	for _, id := range []agentcore.SkillID{"skill.review", "skill.test"} {
		if _, err := skills.Get(ctx, id); !errors.Is(err, skillres.ErrNotFound) {
			t.Fatalf("Get(%s) error = %v, want ErrNotFound", id, err)
		}
		if _, err := store.BuildFileTree(ctx, id); !errors.Is(err, skillres.ErrNotFound) {
			t.Fatalf("BuildFileTree(%s) error = %v, want ErrNotFound", id, err)
		}
	}
}

func TestCommandServiceRollsBackCascadeWhenSkillRegistryDeletionFails(t *testing.T) {
	ctx := context.Background()
	baseSkills := skillres.NewMemoryRegistry()
	registerBundleTestSkill(t, baseSkills, "skill.review", "Review")
	registerBundleTestSkill(t, baseSkills, "skill.test", "Test")
	skills := &failingSkillDeleteRegistry{
		Registry: baseSkills,
		failID:   "skill.test",
		failErr:  errors.New("injected registry delete failure"),
	}
	bundles := skillres.NewMemoryBundleRegistry()
	command := NewCommandService(toolres.NewMemoryRegistry(), skills)
	command.SetSkillBundleRegistry(bundles)
	command.SetAgentProfileBindingStore(&fakeAgentProfileBindingStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}})
	command.SetIDGenerator(&sequenceIDGenerator{values: []string{"skill-bundle-quality"}})
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}
	created, err := command.CreateSkillBundle(ctx, viewer, CreateSkillBundleRequest{
		Name:     "Quality",
		SkillIDs: []string{"skill.review", "skill.test"},
	})
	if err != nil {
		t.Fatalf("CreateSkillBundle: %v", err)
	}

	err = command.DeleteSkillBundle(ctx, viewer, created.BundleID, DeleteSkillBundleRequest{DeleteSkills: true})
	if !errors.Is(err, skills.failErr) {
		t.Fatalf("DeleteSkillBundle error = %v, want injected failure", err)
	}
	if _, err := bundles.Get(ctx, created.BundleID); err != nil {
		t.Fatalf("failed cascade removed bundle: %v", err)
	}
	for _, id := range []agentcore.SkillID{"skill.review", "skill.test"} {
		if _, err := baseSkills.Get(ctx, id); err != nil {
			t.Fatalf("failed cascade removed %s: %v", id, err)
		}
	}
}

func TestCommandServiceRollsBackCascadePackagesWhenMetadataFails(t *testing.T) {
	ctx := context.Background()
	baseSkills := skillres.NewMemoryRegistry()
	skills := &failingSkillDeleteRegistry{
		Registry: baseSkills,
		failID:   "skill.test",
		failErr:  errors.New("injected registry delete failure"),
	}
	bundles := skillres.NewMemoryBundleRegistry()
	store := skillres.NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	command := NewCommandService(toolres.NewMemoryRegistry(), skills)
	command.SetSkillBundleRegistry(bundles)
	command.SetSkillPackageStore(store)
	command.SetAgentProfileBindingStore(&fakeAgentProfileBindingStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}})
	command.SetIDGenerator(&sequenceIDGenerator{values: []string{"skill-bundle-quality"}})
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}
	for _, id := range []string{"skill.review", "skill.test"} {
		pkg := buildSkillPackage(t, id, id, "# Skill\n")
		if _, err := command.ImportSkillPackage(ctx, viewer, bytes.NewReader(pkg), int64(len(pkg))); err != nil {
			t.Fatalf("ImportSkillPackage(%s): %v", id, err)
		}
	}
	created, err := command.CreateSkillBundle(ctx, viewer, CreateSkillBundleRequest{
		Name:     "Quality",
		SkillIDs: []string{"skill.review", "skill.test"},
	})
	if err != nil {
		t.Fatalf("CreateSkillBundle: %v", err)
	}

	err = command.DeleteSkillBundle(ctx, viewer, created.BundleID, DeleteSkillBundleRequest{DeleteSkills: true})
	if !errors.Is(err, skills.failErr) {
		t.Fatalf("DeleteSkillBundle error = %v, want injected failure", err)
	}
	for _, id := range []agentcore.SkillID{"skill.review", "skill.test"} {
		if _, err := store.BuildFileTree(ctx, id); err != nil {
			t.Fatalf("failed cascade removed package %s: %v", id, err)
		}
	}
}

func TestCommandServiceRollsBackCascadeWhenBundleDeletionFails(t *testing.T) {
	ctx := context.Background()
	skills := skillres.NewMemoryRegistry()
	registerBundleTestSkill(t, skills, "skill.review", "Review")
	registerBundleTestSkill(t, skills, "skill.test", "Test")
	baseBundles := skillres.NewMemoryBundleRegistry()
	bundles := &failingBundleDeleteRegistry{
		BundleRegistry: baseBundles,
		failErr:        errors.New("injected bundle delete failure"),
	}
	command := NewCommandService(toolres.NewMemoryRegistry(), skills)
	command.SetSkillBundleRegistry(bundles)
	command.SetAgentProfileBindingStore(&fakeAgentProfileBindingStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}})
	command.SetIDGenerator(&sequenceIDGenerator{values: []string{"skill-bundle-quality"}})
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}
	created, err := command.CreateSkillBundle(ctx, viewer, CreateSkillBundleRequest{
		Name:     "Quality",
		SkillIDs: []string{"skill.review", "skill.test"},
	})
	if err != nil {
		t.Fatalf("CreateSkillBundle: %v", err)
	}

	err = command.DeleteSkillBundle(ctx, viewer, created.BundleID, DeleteSkillBundleRequest{DeleteSkills: true})
	if !errors.Is(err, bundles.failErr) {
		t.Fatalf("DeleteSkillBundle error = %v, want injected failure", err)
	}
	if _, err := baseBundles.Get(ctx, created.BundleID); err != nil {
		t.Fatalf("failed cascade removed bundle: %v", err)
	}
	for _, id := range []agentcore.SkillID{"skill.review", "skill.test"} {
		if _, err := skills.Get(ctx, id); err != nil {
			t.Fatalf("failed cascade removed %s: %v", id, err)
		}
	}
}

func TestCommandServiceRestoresBundleWhenDeleteReturnsErrorAfterMutation(t *testing.T) {
	ctx := context.Background()
	skills := skillres.NewMemoryRegistry()
	registerBundleTestSkill(t, skills, "skill.review", "Review")
	baseBundles := skillres.NewMemoryBundleRegistry()
	bundles := &failingBundleDeleteRegistry{
		BundleRegistry: baseBundles,
		failErr:        errors.New("injected post-delete failure"),
		deleteFirst:    true,
	}
	command := NewCommandService(toolres.NewMemoryRegistry(), skills)
	command.SetSkillBundleRegistry(bundles)
	command.SetAgentProfileBindingStore(&fakeAgentProfileBindingStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}})
	command.SetIDGenerator(&sequenceIDGenerator{values: []string{"skill-bundle-quality"}})
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}
	created, err := command.CreateSkillBundle(ctx, viewer, CreateSkillBundleRequest{
		Name:     "Quality",
		SkillIDs: []string{"skill.review"},
	})
	if err != nil {
		t.Fatalf("CreateSkillBundle: %v", err)
	}

	err = command.DeleteSkillBundle(ctx, viewer, created.BundleID, DeleteSkillBundleRequest{DeleteSkills: true})
	if !errors.Is(err, bundles.failErr) {
		t.Fatalf("DeleteSkillBundle error = %v, want injected failure", err)
	}
	if _, err := baseBundles.Get(ctx, created.BundleID); err != nil {
		t.Fatalf("failed cascade did not restore bundle: %v", err)
	}
	if _, err := skills.Get(ctx, "skill.review"); err != nil {
		t.Fatalf("failed cascade did not restore skill: %v", err)
	}
}

func TestCommandServiceRestoresNonCascadeBundleWhenDeleteReturnsErrorAfterMutation(t *testing.T) {
	ctx := context.Background()
	skills := skillres.NewMemoryRegistry()
	baseBundles := skillres.NewMemoryBundleRegistry()
	bundles := &failingBundleDeleteRegistry{
		BundleRegistry: baseBundles,
		failErr:        errors.New("injected post-delete failure"),
		deleteFirst:    true,
	}
	command := NewCommandService(toolres.NewMemoryRegistry(), skills)
	command.SetSkillBundleRegistry(bundles)
	command.SetIDGenerator(&sequenceIDGenerator{values: []string{"skill-bundle-empty"}})
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}
	created, err := command.CreateSkillBundle(ctx, viewer, CreateSkillBundleRequest{Name: "Empty"})
	if err != nil {
		t.Fatalf("CreateSkillBundle: %v", err)
	}

	err = command.DeleteSkillBundle(ctx, viewer, created.BundleID, DeleteSkillBundleRequest{})
	if !errors.Is(err, bundles.failErr) {
		t.Fatalf("DeleteSkillBundle error = %v, want injected failure", err)
	}
	if _, err := baseBundles.Get(ctx, created.BundleID); err != nil {
		t.Fatalf("failed non-cascade delete did not restore bundle: %v", err)
	}
}

func TestCommandServiceRollsBackCascadeWhenAuditFails(t *testing.T) {
	ctx := context.Background()
	skills := skillres.NewMemoryRegistry()
	registerBundleTestSkill(t, skills, "skill.review", "Review")
	registerBundleTestSkill(t, skills, "skill.test", "Test")
	bundles := skillres.NewMemoryBundleRegistry()
	command := NewCommandService(toolres.NewMemoryRegistry(), skills)
	command.SetSkillBundleRegistry(bundles)
	command.SetAgentProfileBindingStore(&fakeAgentProfileBindingStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}})
	command.SetIDGenerator(&sequenceIDGenerator{values: []string{"skill-bundle-quality"}})
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}
	created, err := command.CreateSkillBundle(ctx, viewer, CreateSkillBundleRequest{
		Name:     "Quality",
		SkillIDs: []string{"skill.review", "skill.test"},
	})
	if err != nil {
		t.Fatalf("CreateSkillBundle: %v", err)
	}
	audit := &failingActionAuditRecorder{
		failAction: "resource.skill_bundle.delete",
		failErr:    errors.New("injected audit failure"),
	}
	command.SetAuditRecorder(audit)

	err = command.DeleteSkillBundle(ctx, viewer, created.BundleID, DeleteSkillBundleRequest{DeleteSkills: true})
	if !errors.Is(err, audit.failErr) {
		t.Fatalf("DeleteSkillBundle error = %v, want injected failure", err)
	}
	if _, err := bundles.Get(ctx, created.BundleID); err != nil {
		t.Fatalf("failed cascade removed bundle: %v", err)
	}
	for _, id := range []agentcore.SkillID{"skill.review", "skill.test"} {
		if _, err := skills.Get(ctx, id); err != nil {
			t.Fatalf("failed cascade removed %s: %v", id, err)
		}
	}
}

func TestCommandServiceRestoresBundleWhenAuditCancelsRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	skills := skillres.NewMemoryRegistry()
	bundles := skillres.NewMemoryBundleRegistry()
	command := NewCommandService(toolres.NewMemoryRegistry(), skills)
	command.SetSkillBundleRegistry(bundles)
	command.SetIDGenerator(&sequenceIDGenerator{values: []string{"skill-bundle-empty"}})
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}
	created, err := command.CreateSkillBundle(ctx, viewer, CreateSkillBundleRequest{Name: "Empty"})
	if err != nil {
		t.Fatalf("CreateSkillBundle: %v", err)
	}
	command.SetAuditRecorder(cancellingAuditRecorder{cancel: cancel})

	err = command.DeleteSkillBundle(ctx, viewer, created.BundleID, DeleteSkillBundleRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("DeleteSkillBundle error = %v, want context canceled", err)
	}
	if _, err := bundles.Get(context.Background(), created.BundleID); err != nil {
		t.Fatalf("audit cancellation did not restore bundle: %v", err)
	}
}

func TestCommandServiceRejectsCascadeWhenAnySkillHasAgentBinding(t *testing.T) {
	ctx := context.Background()
	skills := skillres.NewMemoryRegistry()
	registerBundleTestSkill(t, skills, "skill.review", "Review")
	registerBundleTestSkill(t, skills, "skill.test", "Test")
	bundles := skillres.NewMemoryBundleRegistry()
	profile := agentcore.AgentProfile{ID: "profile-reviewer", SkillRefs: []agentcore.SkillRef{{SkillID: "skill.review"}}}
	profiles := &fakeAgentProfileBindingStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{profile.ID: profile},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID: profile.ID,
			DisplayName:    "Review Agent",
		}}},
	}
	command := NewCommandService(toolres.NewMemoryRegistry(), skills)
	command.SetSkillBundleRegistry(bundles)
	command.SetAgentProfileBindingStore(profiles)
	command.SetIDGenerator(&sequenceIDGenerator{values: []string{"skill-bundle-bound"}})
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}
	created, err := command.CreateSkillBundle(ctx, viewer, CreateSkillBundleRequest{Name: "Bound", SkillIDs: []string{"skill.review", "skill.test"}})
	if err != nil {
		t.Fatalf("CreateSkillBundle: %v", err)
	}

	err = command.DeleteSkillBundle(ctx, viewer, created.BundleID, DeleteSkillBundleRequest{DeleteSkills: true})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("DeleteSkillBundle error = %v, want ErrConflict", err)
	}
	if profiles.query.Placement != agentcore.TalentPlacementFilterAll {
		t.Fatalf("binding preflight placement = %q, want all", profiles.query.Placement)
	}
	if _, err := bundles.Get(ctx, created.BundleID); err != nil {
		t.Fatalf("blocked delete removed bundle: %v", err)
	}
	for _, id := range []agentcore.SkillID{"skill.review", "skill.test"} {
		if _, err := skills.Get(ctx, id); err != nil {
			t.Fatalf("blocked delete removed %s: %v", id, err)
		}
	}
}

func TestCommandServiceRejectsCascadeWhenSkillBelongsToAnotherBundle(t *testing.T) {
	ctx := context.Background()
	skills := skillres.NewMemoryRegistry()
	registerBundleTestSkill(t, skills, "skill.shared", "Shared")
	bundles := skillres.NewMemoryBundleRegistry()
	command := NewCommandService(toolres.NewMemoryRegistry(), skills)
	command.SetSkillBundleRegistry(bundles)
	command.SetAgentProfileBindingStore(&fakeAgentProfileBindingStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}})
	command.SetIDGenerator(&sequenceIDGenerator{values: []string{"skill-bundle-first", "skill-bundle-second"}})
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}
	first, err := command.CreateSkillBundle(ctx, viewer, CreateSkillBundleRequest{Name: "First", SkillIDs: []string{"skill.shared"}})
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}
	if _, err := command.CreateSkillBundle(ctx, viewer, CreateSkillBundleRequest{Name: "Second", SkillIDs: []string{"skill.shared"}}); err != nil {
		t.Fatalf("Create second: %v", err)
	}

	if err := command.DeleteSkillBundle(ctx, viewer, first.BundleID, DeleteSkillBundleRequest{DeleteSkills: true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("DeleteSkillBundle error = %v, want ErrConflict", err)
	}
	if _, err := bundles.Get(ctx, first.BundleID); err != nil {
		t.Fatalf("blocked delete removed first bundle: %v", err)
	}
}

func TestCommandServiceRejectsDirectSkillDeleteWhileBoundOrBundled(t *testing.T) {
	ctx := context.Background()
	skills := skillres.NewMemoryRegistry()
	registerBundleTestSkill(t, skills, "skill.review", "Review")
	bundles := skillres.NewMemoryBundleRegistry()
	profile := agentcore.AgentProfile{ID: "profile-reviewer", SkillRefs: []agentcore.SkillRef{{SkillID: "skill.review"}}}
	profiles := &fakeAgentProfileBindingStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{profile.ID: profile},
		page:     agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{AgentProfileID: profile.ID, DisplayName: "Reviewer"}}},
	}
	command := NewCommandService(toolres.NewMemoryRegistry(), skills)
	command.SetSkillBundleRegistry(bundles)
	command.SetAgentProfileBindingStore(profiles)
	command.SetIDGenerator(&sequenceIDGenerator{values: []string{"skill-bundle-review"}})
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceManage}}
	if _, err := command.CreateSkillBundle(ctx, viewer, CreateSkillBundleRequest{Name: "Review", SkillIDs: []string{"skill.review"}}); err != nil {
		t.Fatalf("CreateSkillBundle: %v", err)
	}

	if err := command.DeleteSkill(ctx, viewer, "skill.review"); !errors.Is(err, ErrConflict) {
		t.Fatalf("DeleteSkill error = %v, want ErrConflict", err)
	}
	if _, err := skills.Get(ctx, "skill.review"); err != nil {
		t.Fatalf("blocked direct delete removed skill: %v", err)
	}
}

func registerBundleTestSkill(t *testing.T, registry skillres.Registry, id agentcore.SkillID, name string) {
	t.Helper()
	if err := registry.Register(context.Background(), skillres.SkillDefinition{
		ID:      id,
		Name:    name,
		Version: "1.0.0",
		Enabled: true,
	}); err != nil {
		t.Fatalf("register skill %s: %v", id, err)
	}
}
