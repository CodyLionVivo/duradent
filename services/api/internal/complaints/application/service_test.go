package application

import (
	"context"
	"testing"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/complaints/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/complaints/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
)

type fakeStore struct {
	seq   map[int]int
	byID  map[id.ID]*domain.Complaint
	byKey map[string]id.ID
}

func newStore() *fakeStore {
	return &fakeStore{seq: map[int]int{}, byID: map[id.ID]*domain.Complaint{}, byKey: map[string]id.ID{}}
}

func (f *fakeStore) NextNumber(_ context.Context, year int) (int, error) {
	f.seq[year]++
	return f.seq[year], nil
}

func (f *fakeStore) Create(_ context.Context, c *domain.Complaint, key string) error {
	if key != "" {
		if _, dup := f.byKey[key]; dup {
			return apperr.Conflict("complaints.duplicate", "Idempotency-Key", "dup")
		}
		f.byKey[key] = c.ID
	}
	cp := *c
	f.byID[c.ID] = &cp
	return nil
}

func (f *fakeStore) SaveAnswer(_ context.Context, c *domain.Complaint) error {
	if f.byID[c.ID].Status != domain.StatusOpen {
		return apperr.Conflict("complaints.already_answered", "status", "answered")
	}
	cp := *c
	f.byID[c.ID] = &cp
	return nil
}

func (f *fakeStore) ByID(_ context.Context, cid id.ID) (*domain.Complaint, error) {
	c, ok := f.byID[cid]
	if !ok {
		return nil, apperr.NotFound("complaints.not_found", "no")
	}
	cp := *c
	return &cp, nil
}

func (f *fakeStore) ByIdempotencyKey(ctx context.Context, key string) (*domain.Complaint, error) {
	cid, ok := f.byKey[key]
	if !ok {
		return nil, apperr.NotFound("complaints.not_found", "no")
	}
	return f.ByID(ctx, cid)
}

func (f *fakeStore) List(context.Context, ports.Filter) ([]*domain.Complaint, int, error) {
	return nil, len(f.byID), nil
}

func (f *fakeStore) CountOpen(_ context.Context, now time.Time) (int, int, error) {
	open, late := 0, 0
	for _, c := range f.byID {
		if c.Status == domain.StatusOpen {
			open++
		}
		if c.Overdue(now) {
			late++
		}
	}
	return open, late, nil
}

type tx struct{}

func (tx) InTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type recorded struct {
	typ     string
	payload any
}

type events struct{ list []recorded }

func (e *events) Record(_ context.Context, _ string, _ id.ID, typ string, payload any, _ time.Time) error {
	e.list = append(e.list, recorded{typ, payload})
	return nil
}

func setup() (*Service, *fakeStore, *events, *time.Time) {
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	store, ev := newStore(), &events{}
	svc := NewService(Deps{Complaints: store, UoW: tx{}, Events: ev, Now: func() time.Time { return now }},
		Config{Provider: domain.Provider{Name: "Duradent S.A.C.", RUC: "20000000000", Address: "Lima"}})
	return svc, store, ev, &now
}

func input() domain.Input {
	return domain.Input{
		Kind: "queja", Name: "Ana Torres", DocType: "DNI", DocNumber: "12345678", Address: "Lima", Email: "ana@clinica.pe",
		ItemType: "servicio", ItemDescription: "Atención en tienda", Detail: "Me atendieron tarde.", Request: "Mejorar la atención.",
		Channel: "email",
	}
}

func TestFileNumbersSheetsAndPublishesThem(t *testing.T) {
	svc, _, ev, _ := setup()
	ctx := context.Background()
	a, err := svc.File(ctx, input(), id.Nil, "k1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.File(ctx, input(), id.Nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Code() != "000001-2026" || b.Code() != "000002-2026" {
		t.Fatalf("numbers %s %s", a.Code(), b.Code())
	}
	again, err := svc.File(ctx, input(), id.Nil, "k1")
	if err != nil || again.ID != a.ID {
		t.Fatalf("a retry must return the first sheet: %v %+v", err, again)
	}
	if len(ev.list) != 2 || ev.list[0].typ != "ComplaintFiled" {
		t.Fatalf("events %+v", ev.list)
	}
	if s := ev.list[0].payload.(Sheet); s.Code != "000001-2026" || s.Email != "ana@clinica.pe" || s.ProviderRUC != "20000000000" || s.AnsweredAt != nil {
		t.Fatalf("payload %+v", s)
	}
}

func TestAnswerPublishesTheAnswerOnce(t *testing.T) {
	svc, _, ev, now := setup()
	ctx := context.Background()
	c, _ := svc.File(ctx, input(), id.Nil, "")
	*now = now.Add(48 * time.Hour)
	got, err := svc.Answer(ctx, c.ID, "Lo sentimos; reforzamos el turno de la mañana.", "jefe@duradent.pe")
	if err != nil || got.Status != domain.StatusAnswered || got.AnsweredBy != "jefe@duradent.pe" {
		t.Fatalf("answer: %v %+v", err, got)
	}
	last := ev.list[len(ev.list)-1]
	if s := last.payload.(Sheet); last.typ != "ComplaintAnswered" || s.AnsweredAt == nil || s.Response == "" {
		t.Fatalf("event %+v", last)
	}
	if _, err := svc.Answer(ctx, c.ID, "otra vez", "x"); !apperr.IsKind(err, apperr.KindConflict) {
		t.Fatalf("second answer: %v", err)
	}
	if _, err := svc.Answer(ctx, id.New(), "x", "x"); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("unknown sheet: %v", err)
	}
}

func TestListCountsOverdueSheets(t *testing.T) {
	svc, _, _, now := setup()
	ctx := context.Background()
	_, _ = svc.File(ctx, input(), id.Nil, "")
	*now = now.AddDate(0, 1, 0)
	p, err := svc.List(ctx, ports.Filter{})
	if err != nil || p.Open != 1 || p.Overdue != 1 {
		t.Fatalf("page %+v %v", p, err)
	}
}
