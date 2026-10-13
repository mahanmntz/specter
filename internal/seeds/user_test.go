package seeds

import (
	"context"
	"testing"
)

type memStore map[string]string

func (m memStore) GetSetting(_ context.Context, k string) (string, bool, error) {
	v, ok := m[k]
	return v, ok, nil
}
func (m memStore) SetSetting(_ context.Context, k, v string) error { m[k] = v; return nil }

func TestKeyAndMerge(t *testing.T) {
	a := SeedTarget{Name: "Stripe", Domain: "stripe.com", ATSService: "Greenhouse", ATSBoardID: "Stripe"}
	b := SeedTarget{Name: "Stripe Inc", Domain: "stripe.com", ATSService: "greenhouse", ATSBoardID: "stripe"}
	g := SeedTarget{Name: "Site", Domain: "Example.com", ATSService: "generic"}
	if Key(a) != Key(b) || Key(g) != "domain:example.com" {
		t.Errorf("keys: %q %q %q", Key(a), Key(b), Key(g))
	}
	if got := Merge([]SeedTarget{a}, []SeedTarget{b, g}); len(got) != 2 || got[0].Name != "Stripe" {
		t.Errorf("merge = %+v", got)
	}
}

func TestEffectiveCombinesEmbeddedUserAndDisabled(t *testing.T) {
	ctx := context.Background()
	st := memStore{}
	base, err := Effective(ctx, st)
	if err != nil || len(base) == 0 {
		t.Fatalf("base seeds: %v %d", err, len(base))
	}

	added, err := AddUser(ctx, st, []SeedTarget{
		{Name: "NewCo", Domain: "newco.com", ATSService: "lever", ATSBoardID: "newco"},
		{Name: "Dup", Domain: "dup.com", ATSService: base[0].ATSService, ATSBoardID: base[0].ATSBoardID}, // already embedded
	})
	if err != nil || added != 1 {
		t.Fatalf("added=%d err=%v", added, err)
	}
	withUser, _ := Effective(ctx, st)
	if len(withUser) != len(base)+1 {
		t.Errorf("want %d seeds, got %d", len(base)+1, len(withUser))
	}

	if err := Disable(ctx, st, []string{Key(base[0]), "lever:newco"}); err != nil {
		t.Fatal(err)
	}
	after, _ := Effective(ctx, st)
	if len(after) != len(base)-1 {
		t.Errorf("disabled seeds must be dropped: want %d, got %d", len(base)-1, len(after))
	}

	if err := Reset(ctx, st); err != nil {
		t.Fatal(err)
	}
	if reset, _ := Effective(ctx, st); len(reset) != len(base) {
		t.Errorf("reset should restore the embedded catalog: %d vs %d", len(reset), len(base))
	}
	if all, err := Effective(ctx, nil); err != nil || len(all) != len(base) {
		t.Errorf("nil store → embedded only: %v %d", err, len(all))
	}
}

func TestCorruptSettingsAreIgnored(t *testing.T) {
	st := memStore{userSeedsKey: "{not json", disabledSeedsKey: "[1,2"}
	if got, err := Effective(context.Background(), st); err != nil || len(got) == 0 {
		t.Errorf("corrupt settings must not break scanning: %v %d", err, len(got))
	}
}
