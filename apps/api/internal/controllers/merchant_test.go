package controllers_test

// Unit tests: the controller runs against in-memory fakes, so no database or file is needed.
// merchant_integration_test.go runs the same use cases on the real repositories.

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"encore.app/internal/apperr"
	"encore.app/internal/controllers"
	"encore.app/internal/entities"
)

type fakeRepo struct {
	calls []string

	registered    []entities.Registration
	maxOwnedSeen  int
	registerErr   error
	markReadyErr  error
	removeErr     error
	markFailedFor []string // reasons
	removed       []string
	removeCtxErr  error // ctx.Err() seen by Remove

	unfinished []entities.TenantRef
	list       []entities.Membership
}

func (f *fakeRepo) Register(_ context.Context, r entities.Registration, max int) (entities.Timestamps, error) {
	f.calls = append(f.calls, "Register")
	f.maxOwnedSeen = max
	if f.registerErr != nil {
		return entities.Timestamps{}, f.registerErr
	}
	f.registered = append(f.registered, r)
	return entities.Timestamps{CreatedAt: time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(100, 0).UTC()}, nil
}

func (f *fakeRepo) MarkReady(context.Context, string, uint) error {
	f.calls = append(f.calls, "MarkReady")
	return f.markReadyErr
}

func (f *fakeRepo) MarkFailed(_ context.Context, _ string, reason string) error {
	f.calls = append(f.calls, "MarkFailed")
	f.markFailedFor = append(f.markFailedFor, reason)
	return nil
}

func (f *fakeRepo) Remove(ctx context.Context, id string) error {
	f.calls = append(f.calls, "Remove")
	f.removeCtxErr = ctx.Err()
	if f.removeErr != nil {
		return f.removeErr
	}
	f.removed = append(f.removed, id)
	return nil
}

func (f *fakeRepo) ListForUser(context.Context, string) ([]entities.Membership, error) {
	f.calls = append(f.calls, "ListForUser")
	return f.list, nil
}

func (f *fakeRepo) FindUnfinished(context.Context, time.Time) ([]entities.TenantRef, error) {
	f.calls = append(f.calls, "FindUnfinished")
	return f.unfinished, nil
}

type fakeTenants struct {
	calls        []string
	provisionErr error
	provisionFn  func() // runs inside Provision, e.g. to cancel the context
	unsafe       map[string]bool
	discarded    []string
	version      uint
}

func (f *fakeTenants) Provision(_ context.Context, _ string, _ entities.TenantProfile) (uint, error) {
	f.calls = append(f.calls, "Provision")
	if f.provisionFn != nil {
		f.provisionFn()
	}
	if f.provisionErr != nil {
		return 0, f.provisionErr
	}
	return f.version, nil
}

func (f *fakeTenants) Discard(path string) error {
	f.calls = append(f.calls, "Discard")
	if f.unsafe[path] {
		return errors.New("unsafe")
	}
	f.discarded = append(f.discarded, path)
	return nil
}

func setup(o controllers.Options) (*controllers.MerchantController, *fakeRepo, *fakeTenants) {
	r := &fakeRepo{}
	t := &fakeTenants{version: 3}
	return controllers.NewMerchantController(r, t, o), r, t
}

var validReq = entities.MerchantRequest{UserID: "u1", Name: "Shop", Slug: "shop"}

func TestCreateRunsTheStepsInOrder(t *testing.T) {
	c, r, tn := setup(controllers.Options{})
	m, err := c.Create(context.Background(), validReq)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Provision"}; !reflect.DeepEqual(tn.calls, want) {
		t.Errorf("tenant calls = %v, want %v", tn.calls, want)
	}
	if want := []string{"Register", "MarkReady"}; !reflect.DeepEqual(r.calls, want) {
		t.Errorf("repo calls = %v, want %v", r.calls, want)
	}
	reg := r.registered[0]
	if m.ID != reg.MerchantID || m.Slug != "shop" || m.SchemaVersion != 3 {
		t.Errorf("merchant = %+v", m)
	}
	if !m.CreatedAt.Equal(time.Unix(100, 0)) || !m.UpdatedAt.Equal(time.Unix(100, 0)) {
		t.Errorf("times = %v, %v, want the ones the repository returned", m.CreatedAt, m.UpdatedAt)
	}
	if reg.DBPath != entities.TenantDBPath(reg.MerchantID) || reg.OwnerID != "u1" || reg.ShardID != "s1" || reg.MemberID == "" {
		t.Errorf("registration = %+v", reg)
	}
}

func TestMaxOwnedOption(t *testing.T) {
	for _, tt := range []struct{ in, want int }{{0, 5}, {2, 2}, {-1, 0}} {
		c, r, _ := setup(controllers.Options{MaxOwned: tt.in})
		if _, err := c.Create(context.Background(), validReq); err != nil {
			t.Fatal(err)
		}
		if r.maxOwnedSeen != tt.want {
			t.Errorf("MaxOwned %d reached the repo as %d, want %d", tt.in, r.maxOwnedSeen, tt.want)
		}
	}
}

func TestInvalidRequestTouchesNothing(t *testing.T) {
	c, r, tn := setup(controllers.Options{})
	_, err := c.Create(context.Background(), entities.MerchantRequest{UserID: "u", Name: "", Slug: "x"})
	if !apperr.IsKind(err, apperr.InvalidArgument) {
		t.Fatalf("got %v, want InvalidArgument", err)
	}
	if len(r.calls)+len(tn.calls) != 0 {
		t.Fatalf("calls = %v %v, want none", r.calls, tn.calls)
	}
}

func TestRegisterErrorIsReturnedAsIsAndNothingIsRolledBack(t *testing.T) {
	c, r, tn := setup(controllers.Options{})
	r.registerErr = entities.ErrSlugTaken
	if _, err := c.Create(context.Background(), validReq); !errors.Is(err, entities.ErrSlugTaken) {
		t.Fatalf("got %v, want ErrSlugTaken", err)
	}
	if len(tn.discarded) != 0 || len(r.removed) != 0 {
		t.Fatalf("nothing was written, so nothing may be cleaned up: %v %v", tn.discarded, r.removed)
	}
}

func TestProvisionFailureRollsBack(t *testing.T) {
	boom := apperr.Wrap(errors.New("disk full"), apperr.Internal, "internal error")
	for name, setFail := range map[string]func(*fakeRepo, *fakeTenants){
		"provision":  func(_ *fakeRepo, tn *fakeTenants) { tn.provisionErr = boom },
		"mark ready": func(r *fakeRepo, _ *fakeTenants) { r.markReadyErr = boom },
	} {
		t.Run(name, func(t *testing.T) {
			c, r, tn := setup(controllers.Options{})
			setFail(r, tn)
			_, err := c.Create(context.Background(), validReq)
			if !errors.Is(err, boom) {
				t.Fatalf("got %v, want the original failure", err)
			}
			id := r.registered[0].MerchantID
			if !reflect.DeepEqual(tn.discarded, []string{entities.TenantDBPath(id)}) || !reflect.DeepEqual(r.removed, []string{id}) {
				t.Fatalf("file %v and rows %v must both be removed", tn.discarded, r.removed)
			}
			if len(r.markFailedFor) != 0 {
				t.Fatalf("cleanup worked, nothing to mark as failed")
			}
		})
	}
}

func TestRollbackSurvivesACancelledRequest(t *testing.T) {
	c, r, tn := setup(controllers.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	tn.provisionFn = cancel
	tn.provisionErr = context.Canceled
	if _, err := c.Create(ctx, validReq); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if len(r.removed) != 1 || r.removeCtxErr != nil {
		t.Fatalf("cleanup must run on a live context: removed=%v ctxErr=%v", r.removed, r.removeCtxErr)
	}
}

func TestFailedCleanupMarksTheRowForTheReaper(t *testing.T) {
	c, r, tn := setup(controllers.Options{})
	tn.provisionErr = errors.New("copy failed")
	r.removeErr = errors.New("database is locked")
	if _, err := c.Create(context.Background(), validReq); err == nil {
		t.Fatal("want an error")
	}
	if len(r.markFailedFor) != 1 || r.markFailedFor[0] != "copy failed" {
		t.Fatalf("MarkFailed reasons = %v", r.markFailedFor)
	}
}

func TestReapStaleSkipsUnsafePaths(t *testing.T) {
	c, r, tn := setup(controllers.Options{})
	r.unfinished = []entities.TenantRef{
		{MerchantID: "a", DBPath: "tenants/a.db"},
		{MerchantID: "evil", DBPath: "../outside.db"},
		{MerchantID: "b", DBPath: "tenants/b.db"},
	}
	tn.unsafe = map[string]bool{"../outside.db": true}
	n, err := c.ReapStale(context.Background(), 15*time.Minute)
	if err != nil || n != 2 {
		t.Fatalf("reaped %d, %v; want 2", n, err)
	}
	if !reflect.DeepEqual(r.removed, []string{"a", "b"}) {
		t.Fatalf("removed %v", r.removed)
	}
}

func TestListForUserNeedsAUser(t *testing.T) {
	c, r, _ := setup(controllers.Options{})
	if _, err := c.ListForUser(context.Background(), ""); !apperr.IsKind(err, apperr.Unauthenticated) {
		t.Fatalf("got %v, want Unauthenticated", err)
	}
	r.list = []entities.Membership{{MerchantID: "m", Slug: "s", Name: "n", Role: entities.RoleOwner}}
	got, err := c.ListForUser(context.Background(), "u")
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
}
